package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"reflect"

	"path/filepath"

	"net/http"
	"net/url"

	"github.com/adrg/xdg"
	"github.com/mmcdole/gofeed"
	"gopkg.in/yaml.v3"
)

const appName = "feed-trigger"

var dataDirPath = filepath.Join(xdg.DataHome, appName)
var configDirPath = filepath.Join(xdg.ConfigHome, appName)

func main() {
	exitCode := 0
	err := prepareAppDirs()
	if err != nil {
		log.Fatal(fmt.Errorf("Failed to prepare app directories. Caused by %w", err))
	}
	config, err := readConfig()
	if err != nil {
		log.Fatal(fmt.Errorf("Failed to read a config. Caused by %w", err))
	}
	feedParser := gofeed.NewParser()
	var httpClient http.Client
	for i := 0; i < len(config.Feeds); i++ {
		err := eachFeed(httpClient, *feedParser, *config, config.Feeds[i])
		if err != nil {
			log.Println(err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}

func prepareAppDirs() error {
	dirs := []string{dataDirPath, configDirPath}
	for i := 0; i < len(dirs); i++ {
		dir := dirs[i]
		file, err := os.Open(dir)
		if os.IsNotExist(err) {
			err := os.Mkdir(dir, os.ModeDir|0755)
			if err != nil {
				return fmt.Errorf("Failed to create a directory: %s. Caused by %w", dir, err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("Failed to open a directory: %s. Caused by %w", dir, err)
		}
		file.Close()
	}
	return nil
}

func eachFeed(httpClient http.Client, feedParser gofeed.Parser, config Config, feedUrl string) error {
	feedReader, err := download(httpClient, feedUrl)
	if err != nil {
		return fmt.Errorf("Failed to download a feed: %s. Caused by %w", feedUrl, err)
	}
	defer feedReader.Close()
	var tempFeedFile, prevFeedFile *os.File
	ret, err := func() (bool, error) {
		tempFeedFile, err = os.CreateTemp(dataDirPath, "")
		if err != nil {
			return true, fmt.Errorf("Failed to create a temporary feed file. Caused by %w", err)
		}
		defer tempFeedFile.Close()
		teedFeedReader := io.TeeReader(feedReader, tempFeedFile)
		feed, err := feedParser.Parse(teedFeedReader)
		if err != nil {
			return true, fmt.Errorf("Failed to parse a feed. Caused by %w", err)
		}
		newFeed := *feed
		ret, err := func() (bool, error) {
			prevFeedFile, err = os.Open(makeFeedPath(feedUrl))
			if err != nil {
				if !os.IsNotExist(err) {
					return true, fmt.Errorf("Failed to open a previous feed file. Caused by %w", err)
				}
			} else {
				defer prevFeedFile.Close()
				prevFeed, err := feedParser.Parse(prevFeedFile)
				if err != nil {
					return true, fmt.Errorf("Failed to parse a previous feed. Caused by %w", err)
				}
				newFeed = subtractFeed(*feed, *prevFeed)
				if len(newFeed.Items) == 0 {
					return true, nil
				}
			}
			return false, nil
		}()
		if err != nil {
			return true, err
		}
		if ret {
			return true, nil
		}
		cmd := exec.Command(config.Run[0], config.Run[1:]...)
		stdinPipe, err := cmd.StdinPipe()
		if err != nil {
			return true, fmt.Errorf("Failed to open a standard-in pipe. Caused by %w", err)
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		var chanErr error
		go func() {
			defer stdinPipe.Close()
			err = json.NewEncoder(stdinPipe).Encode(newFeed) // ここ本来のフォーマットでエンコードしたいけどいい感じのライブラリーがないのでこのまま
			if err != nil {
				chanErr = fmt.Errorf("Failed to write a feed to a standard-in pipe. Caused by %w", err)
			}
		}()
		err = cmd.Run()
		if err != nil {
			return true, fmt.Errorf("Failed to run a command. Caused by %w", err)
		}
		if chanErr != nil {
			return true, chanErr
		}
		return false, nil
	}()
	if err != nil {
		if tempFeedFile != nil {
			_ = os.Remove(tempFeedFile.Name())
		}
		return err
	}
	if ret {
		return nil
	}
	err = os.Rename(tempFeedFile.Name(), makeFeedPath(feedUrl))
	if err != nil {
		return fmt.Errorf("Failed to move a temporary feed file. Caused by %w", err)
	}
	return nil
}

type Config struct {
	Feeds []string
	Run   []string
}

func readConfig() (*Config, error) {
	configPath := filepath.Join(xdg.ConfigHome, appName, "config.yaml")
	configFile, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to open a config: %s. Caused by %w", configPath, err)
	}
	defer configFile.Close()
	var config Config
	err = yaml.NewDecoder(configFile).Decode(&config)
	if err != nil {
		return nil, fmt.Errorf("Failed to unmarshal a config: %s. Caused by %w", configPath, err)
	}
	if len(config.Run) < 1 {
		return nil, fmt.Errorf("\"run\" field must have some strings: %s. Caused by %w", configPath, err)
	}
	return &config, nil
}

func makeFeedPath(url_ string) string {
	return filepath.Join(dataDirPath, url.QueryEscape(url_)+".xml")
}

func download(client http.Client, url string) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed to create a HTTP request. Caused by %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Failed to request via HTTP. Caused by %w", err)
	}
	if resp.StatusCode/100 != 2 {
		bodyBytes, err := io.ReadAll(resp.Request.Body)
		if err == nil {
			return nil, fmt.Errorf(`A status code for an HTTP post is not 200: %s: "%s".`, resp.Status, string(bodyBytes))
		} else {
			return nil, fmt.Errorf("A status code for an HTTP post is not 200: %s.", resp.Status)
		}
	}
	return resp.Body, nil
}

// 新規の方に前のよりも古いアイテムが含まれてる場合は目をつぶる
func subtractFeed(left, right gofeed.Feed) gofeed.Feed {
	var result gofeed.Feed
	result = left
	result.Items = make([]*gofeed.Item, 0, len(left.Items))
left:
	for i := 0; i < len(left.Items); i++ {
		for j := 0; j < len(right.Items); j++ {
			if reflect.DeepEqual(left.Items[i], right.Items[j]) {
				continue left
			}
		}
		result.Items = append(result.Items, left.Items[i])
	}
	return result
}
