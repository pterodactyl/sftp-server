package main

import (
	"errors"
	"flag"
	"github.com/patrickmn/go-cache"
	"github.com/pterodactyl/sftp-server/src/logger"
	"github.com/pterodactyl/sftp-server/src/server"
	"go.uber.org/zap"
	"io/ioutil"
	"os"
	"path"
	"time"
)

func main() {
	var (
		configLocation string
		bindPort       int
		bindAddress    string
		readOnlyMode   bool
		debugMode      bool
	)

	flag.StringVar(&configLocation, "config-path", "./config/core.json", "the location of your Daemon configuration file")
	flag.IntVar(&bindPort, "port", 2022, "the port this server should bind to")
	flag.StringVar(&bindAddress, "bind-addr", "0.0.0.0", "the address this server should bind to")
	flag.BoolVar(&readOnlyMode, "readonly", false, "determines if this server should run in read-only mode")
	flag.BoolVar(&debugMode, "debug", false, "determines if the server should output debug information")
	flag.Parse()

	logger.Initialize(debugMode)

	logger.Get().Infow("reading configuration from path", zap.String("config-path", configLocation))

	config, err := readConfiguration(configLocation)
	if err != nil {
		logger.Get().Fatalw("could not read configuration", zap.Error(err))
	}

	c := cache.New(5 * time.Minute, 10 * time.Minute)

	var s = server.Configuration{
		Data: config,
		Cache: c,
		Settings: server.Settings{
			BasePath:         path.Dir(configLocation),
			ReadOnly:         readOnlyMode,
			BindAddress:      bindAddress,
			BindPort:         bindPort,
			ServerDataFolder: path.Join(path.Dir(configLocation), "/servers"),
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
