package main

import (
	"errors"
	"flag"
	"github.com/pterodactyl/sftp-server/src/logger"
	"github.com/pterodactyl/sftp-server/src/server"
	"go.uber.org/zap"
	"io/ioutil"
	"os"
	"path"
)

func main() {
	var (
		configLocation string
		readOnlyMode bool
		debugMode bool
	)

	flag.StringVar(&configLocation, "config-path", "./config/core.json", "the location of your Daemon configuration file")
	flag.BoolVar(&readOnlyMode, "read-only", false, "determines if this server should run in read-only mode")
	flag.BoolVar(&debugMode, "debug", false, "determines if the server should output debug information")
	flag.Parse()

	logger.Initialize()

	if debugMode == true {
		logger.Get().Infow("running server in debug mode")
	}

	if readOnlyMode == true {
		logger.Get().Infow("running server in read-only mode")
	}

	logger.Get().Infow("reading configuration from path", zap.String("config-path", configLocation))


	c, err := readConfiguration(configLocation)
	if err != nil {
		logger.Get().Fatalw("could not read configuration", zap.Error(err))
	}

	var s = server.Configuration{
		Data: c,
		Settings: server.Settings{
			BasePath: path.Dir(configLocation),
			Debug: debugMode,
			ReadOnly: readOnlyMode,
		},
	}

	if err := s.Initalize(); err != nil {
		logger.Get().Fatalw("could not start SFTP server", zap.Error(err))
	}
}

func readConfiguration(path string) ([]byte, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, errors.New("could not locate a configuration file at the specified path")
	}

	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	return data, nil
}