package main

import (
	"flag"
	"github.com/pterodactyl/sftp-server/src/logger"
	"github.com/pterodactyl/sftp-server/src/server"
	"go.uber.org/zap"
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

	if err := server.Configure(server.Configuration{
		Path: configLocation,
		ReadOnly: readOnlyMode,
		Debug: debugMode,
	}); err != nil {
		logger.Get().Fatalw("fatal error starting SFTP server", zap.Error(err))
	}
}
