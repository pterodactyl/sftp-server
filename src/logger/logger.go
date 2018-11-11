package logger

import (
	"go.uber.org/zap"
)

var sugar *zap.SugaredLogger

// Creates a logger instance.
func Initialize(debug bool) (error) {
	var cfg = zap.Config{}
	if debug {
		cfg = zap.NewDevelopmentConfig()
	} else {
		cfg = zap.NewProductionConfig()
	}

	cfg.Encoding = "console"
	cfg.OutputPaths = []string{
		"stdout",
		"./sftp-server.log",
	}

	logger, err := cfg.Build()
	if err != nil {
		return err
	}
	defer logger.Sync()

	sugar = logger.Sugar()
	return nil
}

// Returns an instance of the logger defined for the SFTP server.
func Get() *zap.SugaredLogger {
	return sugar
}