package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/buger/jsonparser"
	"github.com/patrickmn/go-cache"
	"github.com/pterodactyl/sftp-server/src/logger"
	"github.com/pterodactyl/sftp-server/src/server"
	"go.uber.org/zap"
	"io/ioutil"
	"os"
	"os/user"
	"path"
	"runtime"
	"strconv"
	"time"
)

func main() {
	if runtime.GOOS != "linux" {
		fmt.Printf("This operating system (%s) is not supported.\n", runtime.GOOS)
		os.Exit(1)
	}

	var (
		configLocation   string
		bindPort         int
		bindAddress      string
		readOnlyMode     bool
		debugMode        bool
		disableDiskCheck bool
	)

	flag.StringVar(&configLocation, "config-path", "./config/core.json", "the location of your Daemon configuration file")
	flag.IntVar(&bindPort, "port", 2022, "the port this server should bind to")
	flag.StringVar(&bindAddress, "bind-addr", "0.0.0.0", "the address this server should bind to")
	flag.BoolVar(&readOnlyMode, "readonly", false, "determines if this server should run in read-only mode")
	flag.BoolVar(&disableDiskCheck, "disable-disk-check", false, "determines if disk space checking should be disabled")
	flag.BoolVar(&debugMode, "debug", false, "determines if the server should output debug information")
	flag.Parse()

	logger.Initialize(debugMode)

	logger.Get().Infow("reading configuration from path", zap.String("config-path", configLocation))

	config, err := readConfiguration(configLocation)
	if err != nil {
		logger.Get().Fatalw("could not read configuration", zap.Error(err))
	}

	username, err := jsonparser.GetString(config, "docker", "container", "username")
	if err != nil {
		logger.Get().Debugw("could not find sftp user definition, falling back to \"pterodactyl\"", zap.Error(err))
		username = "pterodactyl"
	}

	logger.Get().Infow("using system daemon user", zap.String("username", username))

	u, err := user.Lookup(username)
	if err != nil {
		logger.Get().Fatalw("failed to lookup sftp user", zap.Error(err))
		return
	}

	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)

	var s = server.Configuration{
		Data:  config,
		Cache: cache.New(5*time.Minute, 10*time.Minute),
		User:  server.SftpUser{
			Uid: uid,
			Gid: gid,
		},
		Settings: server.Settings{
			BasePath:         path.Dir(configLocation),
			ReadOnly:         readOnlyMode,
			BindAddress:      bindAddress,
			BindPort:         bindPort,
			ServerDataFolder: path.Join(path.Dir(configLocation), "/servers"),
			DisableDiskCheck: disableDiskCheck,
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
