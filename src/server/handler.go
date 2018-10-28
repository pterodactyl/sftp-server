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
	"sync"
)

type FileSystem struct {
	directory string
	lock      sync.Mutex
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
	path, err := fs.buildPath(request.Filepath)
	if err != nil {
		return nil, sftp.ErrSshFxNoSuchFile
	}

	fs.lock.Lock()
	defer fs.lock.Unlock()

	file, err := os.OpenFile(path, os.O_RDONLY, 0644)
	if err == os.ErrNotExist {
		return nil, sftp.ErrSshFxNoSuchFile
	} else if err != nil {
		logger.Get().Errorw("could not open file for reading", zap.String("source", path), zap.Error(err))
		return nil, sftp.ErrSshFxFailure
	}

	return file, nil
}

func (fs FileSystem) Filewrite(request *sftp.Request) (io.WriterAt, error) {
	return nil, errors.New("not implemented")
}

// Hander for basic SFTP system calls related to files, but not anything to do with reading
// or writing to those files.
func (fs FileSystem) Filecmd(request *sftp.Request) error {
	path, err := fs.buildPath(request.Filepath)
	if err != nil {
		return sftp.ErrSshFxNoSuchFile
	}

	var target string
	// If a target is provided in this request validate that it is going to the correct
	// location for the server. If it is not, return an operation unsupported error. This
	// is maybe not the best error response, but its not wrong either.
	if request.Target != "" {
		target, err = fs.buildPath(request.Target)
		if err != nil {
			return sftp.ErrSshFxOpUnsupported
		}
	}

	switch request.Method {
	// Need to add this in eventually, should work similarly to the current daemon.
	case "SetStat", "Setstat":
		return sftp.ErrSshFxOpUnsupported
	case "Rename":
		if err := os.Rename(path, target); err != nil {
			logger.Get().Errorw("failed to rename file",
				zap.String("source", path),
				zap.String("target", target),
				zap.Error(err),
			)
			return sftp.ErrSshFxFailure
		}

		return sftp.ErrSshFxOk
	case "Rmdir":
		if err := os.RemoveAll(path); err != nil {
			logger.Get().Errorw("failed to remove directory", zap.String("source", path), zap.Error(err))
			return sftp.ErrSshFxFailure
		}

		return sftp.ErrSshFxOk
	case "Mkdir":
		if err := os.MkdirAll(path, 0755); err != nil {
			logger.Get().Errorw("failed to create directory", zap.String("source", path), zap.Error(err))
			return sftp.ErrSshFxFailure
		}

		return sftp.ErrSshFxOk
	case "Symlink":
		if err := os.Symlink(path, target); err != nil {
			logger.Get().Errorw("failed to create symlink",
				zap.String("source", path),
				zap.String("target", target),
				zap.Error(err),
			)
			return sftp.ErrSshFxFailure
		}

		return sftp.ErrSshFxOk
	case "Remove":
		if err := os.Remove(path); err != nil {
			logger.Get().Errorw("failed to remove a file", zap.String("source", path), zap.Error(err))
			return sftp.ErrSshFxFailure
		}

		return sftp.ErrSshFxOk
	default:
		return sftp.ErrSshFxOpUnsupported
	}
}

// Handler for SFTP filesystem list calls. This will handle calls to list the contents of
// a directory as well as perform file/folder stat calls.
func (fs FileSystem) Filelist(request *sftp.Request) (sftp.ListerAt, error) {
	path, err := fs.buildPath(request.Filepath)
	if err != nil {
		return nil, sftp.ErrSshFxNoSuchFile
	}

	switch request.Method {
	case "List":
		files, err := ioutil.ReadDir(path)
		if err != nil {
			logger.Get().Error("error listing directory", zap.Error(err))
			return nil, sftp.ErrSshFxFailure
		}

		return ListerAt(files), nil
	case "Stat":
		file, err := os.Open(path)
		defer file.Close()

		if err != nil {
			logger.Get().Error("error opening file for stat", zap.Error(err))
			return nil, sftp.ErrSshFxFailure
		}

		s, err := file.Stat()
		if err != nil {
			logger.Get().Error("error statting file", zap.Error(err))
			return nil, sftp.ErrSshFxFailure
		}

		return ListerAt([]os.FileInfo{s}), nil
		// Before adding readlink support we need to evaluate any potential security risks
		// as a result of navigating around to a location that is outside the home directory
		// for the logged in user. I don't forsee it being much of a problem, but I do want to
		// check it out before slapping some code here. Until then, we'll just return an
		// unsupported response code.
		//
		// case "Readlink":
	default:
		return nil, sftp.ErrSshFxOpUnsupported
	}
}

// Normalizes a directory we get from the SFTP request to ensure the user is not able to escape
// from their data directory. After normalization if the directory is still within their home
// path it is returned. If they managed to "escape" an error will be returned.
func (fs FileSystem) buildPath(rawPath string) (string, error) {
	path := filepath.Clean(filepath.Join(fs.directory, rawPath))

	if !strings.HasPrefix(path, fs.directory) {
		return "", errors.New("invalid path resolution")
	}

	return path, nil
}
