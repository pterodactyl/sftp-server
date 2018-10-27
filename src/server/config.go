package server

import (
	"errors"
	"io/ioutil"
	"os"
)

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