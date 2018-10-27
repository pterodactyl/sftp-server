package logger

import "go.uber.org/zap"

var sugar *zap.SugaredLogger

// Creates a logger instance.
func Initialize() (error) {
	logger, err := zap.NewDevelopment()
	defer logger.Sync()
	if err != nil {
		return err
	}

	sugar = logger.Sugar()

	return nil
}

// Returns an instance of the logger defined for the SFTP server.
func Get() *zap.SugaredLogger {
	return sugar
}