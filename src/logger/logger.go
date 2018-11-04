package logger

import (
	"go.uber.org/zap"
)

var sugar *zap.SugaredLogger

// Creates a logger instance.
func Initialize(debug bool) (error) {
	var logger *zap.Logger
	var err error

	if debug {
		logger, err = zap.NewDevelopment()
	} else {
		logger, err = zap.NewProduction()
	}

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