package main

import (
	"encoding/xml"
	"io"
	"log"
	"os"
	"os/exec"

	"path/filepath"

	"net/url"

	"github.com/adrg/xdg"
	"github.com/wbernest/atom-parser"
	"gopkg.in/yaml.v3"
)

const appName = "feed-trigger"

var dataPath = filepath.Join(xdg.DataHome, appName)

func main() {
	exitCode := 0
	config, err := readConfig()
	if err != nil {
		log.Fatal(Error{"Failed to read a config.", err})
	}
	for i := 0; i < len(config.Feeds); i++ {
		err := eachFeed(config, config.Feeds[i])
		if err != nil {
			log.Println(err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}

func eachFeed(config *Config, feedUrl string) error {
	feed, feedText, err := atomparser.ParseURL(feedUrl)
	if err != nil {
		return Error{"Failed to parse a feed.", err}
	}
	newFeed := feed
	prevFeedText, err := readFeed(feedUrl)
	if err != nil {
		return Error{"Failed to read a feed.", err}
	}
	if prevFeedText != nil {
		prevFeed, err := atomparser.ParseString(*prevFeedText)
		if err != nil {
			return Error{"Failed to parse a feed.", err}
		}
		newEntries := atomparser.CompareItemsBetweenOldAndNew(prevFeed, feed)
		if len(newEntries) == 0 {
			return nil
		}
		newFeed.Entry = newEntries
	}
	newFeedBytes, err := xml.Marshal(newFeed)
	if err != nil {
		return Error{"Failed to marshal a feed.", err}
	}
	cmd := exec.Command(config.Run[0], config.Run[1:]...)
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return Error{"Failed to open a standard-in pipe.", err}
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	var chanErr error
	go func() {
		defer stdinPipe.Close()
		_, err = stdinPipe.Write(newFeedBytes) // 手抜き
		if err != nil {
			chanErr = Error{"Failed to write a feed to a standard-in pipe.", err}
		}
	}()
	err = cmd.Run()
	if err != nil {
		return Error{"Failed to run a command.", err}
	}
	if chanErr != nil {
		return chanErr
	}
	err = writeFeed("https://kakkun61.hatenablog.com/feed", feedText)
	if err != nil {
		return Error{"Failed to write a feed.", err}
	}
	return nil
}

type Config struct {
	Feeds []string
	Run   []string
}

func readConfig() (*Config, error) {
	configPath := filepath.Join(xdg.ConfigHome, appName, "config.yaml")
	configFile, err := openFileAndCreateIfNecessaryRecursive(configPath, os.O_RDONLY, 0777)
	if err != nil {
		return nil, Error{"Failed to open a config: " + configPath, err}
	}
	defer configFile.Close()
	configBytes, err := io.ReadAll(configFile)
	if err != nil {
		return nil, Error{"Failed to read a config: " + configPath, err}
	}
	config, err := unmarshalConfig(configBytes)
	if err != nil {
		return nil, Error{"Failed to unmarshal a config: " + configPath, err}
	}
	if len(config.Run) < 1 {
		return nil, Error{"\"run\" field must have some strings: " + configPath, err}
	}
	return config, nil
}

func unmarshalConfig(bytes []byte) (*Config, error) {
	var config Config
	err := yaml.Unmarshal(bytes, &config)
	if err != nil {
		return nil, Error{"Failed to unmarshal the config.", err}
	}
	return &config, nil
}

func makeFeedPath(url_ string) string {
	return filepath.Join(dataPath, url.QueryEscape(url_)+".xml")
}

func writeFeed(url string, content string) error {
	file, err := openFileAndCreateIfNecessaryRecursive(makeFeedPath(url), os.O_WRONLY, 0777)
	if err != nil {
		return Error{"Failed to open a feed file.", err}
	}
	defer file.Close()
	_, err = file.Write([]byte(content)) // 手抜き
	if err != nil {
		return Error{"Failed to write a feed file.", err}
	}
	return nil
}

func readFeed(url string) (*string, error) {
	bytes, err := (os.ReadFile(makeFeedPath(url)))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, Error{"Failed to read a file.", err}
	}
	text := string(bytes)
	return &text, nil
}

func openFileAndCreateIfNecessaryRecursive(path string, flag int, mode os.FileMode) (*os.File, error) {
	file, err := os.OpenFile(path, flag, mode)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, Error{path + ": Failed to open the file.", err}
		} else {
			file, err = os.Create(path)
			if err != nil {
				if !os.IsNotExist(err) {
					return nil, Error{path + ": Failed to create the file.", err}
				}
				dir := filepath.Dir(path)
				err = os.MkdirAll(dir, 0755)
				if err != nil {
					return nil, Error{dir + ": Failed to create the directory.", err}
				}
				file, err = os.Create(path)
				if err != nil {
					return nil, Error{path + ": Failed to create the file after creating the directory.", err}
				}
			}
		}
	}
	return file, nil
}

type Error struct {
	Message string
	Origin  error
}

func (e Error) Error() string {
	if e.Origin == nil {
		return e.Message
	}
	return e.Message + " Caused by: " + e.Origin.Error()
}
