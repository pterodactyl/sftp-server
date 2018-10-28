package server

import (
	"github.com/pkg/errors"
	"github.com/pkg/sftp"
	"github.com/pterodactyl/sftp-server/src/logger"
	"go.uber.org/zap"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

type FileSystem struct {
	directory string
}

// Creates a new SFTP handler for a given server. The directory argument should
// be the base directory for a server. All actions done on the server will be
// relative to that directory, and the user will not be able to escape out of it.
func CreateHandler(directory string) sftp.Handlers {
	p := FileSystem{directory: directory}

	return sftp.Handlers{
		FileGet:  p,
		FilePut:  p,
		FileCmd:  p,
		FileList: p,
	}
}

func (fs FileSystem) Fileread(request *sftp.Request) (io.ReaderAt, error) {
	return nil, errors.New("not implemented")
}

func (fs FileSystem) Filewrite(request *sftp.Request) (io.WriterAt, error) {
	return nil, errors.New("not implemented")
}

func (fs FileSystem) Filecmd(request *sftp.Request) error {
	return errors.New("not implemented")
}

// Handler for SFTP filesystem list calls. This will handle calls to list the contents of
// a directory as well as perform file/folder stat calls.
func (fs FileSystem) Filelist(request *sftp.Request) (sftp.ListerAt, error) {
	d := fs.buildPath(request.Filepath)

	switch request.Method {
	case "List":
		files, err := ioutil.ReadDir(d)
		if err != nil {
			logger.Get().Error("error listing directory?")
			return nil, err
		}

		return ListerAt(files), nil
	case "Stat":
		file, err := os.Open(d)
		defer file.Close()

		if err != nil {
			logger.Get().Error("error opening file for stat", zap.Error(err))
			return nil, err
		}

		s, err := file.Stat()
		if err != nil {
			logger.Get().Error("error statting file", zap.Error(err))
			return nil, err
		}

		return ListerAt([]os.FileInfo{s}), nil
	// case "Readlink":
	default:
		return nil, sftp.ErrSshFxOpUnsupported
	}
}

// Normalizes a directory we get from the SFTP request to ensure the user is not able to escape
// from their data directory. After normalization if the directory is still within their home
// path it is returned. If they managed to "escape", their home path is returned.
//
// Effectively, if the directory is outside of their home folder their home path is returned so
// that it appears they've just reached their top-most directory.
func (fs FileSystem) buildPath(path string) string {
	p := filepath.Clean(filepath.Join(fs.directory, path))

	if !strings.HasPrefix(p, fs.directory) {
		return fs.directory
	}

	return p
}
