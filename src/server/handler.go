package server

import (
	"github.com/buger/jsonparser"
	"github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
	"github.com/pkg/sftp"
	"github.com/pterodactyl/sftp-server/src/logger"
	"go.uber.org/zap"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

type FileSystem struct {
	ServerConfig     string
	Directory        string
	UUID             string
	Permissions      []string
	ReadOnly         bool
	DisableDiskCheck bool
	User             SftpUser
	Cache            *cache.Cache
	lock             sync.Mutex
}

// Creates a reader for a file on the system and returns the reader back.
func (fs FileSystem) Fileread(request *sftp.Request) (io.ReaderAt, error) {
	// Check first if the user can actually open and view a file. This permission is named
	// really poorly, but it is checking if they can read. There is an addition permission,
	// "save-files" which determines if they can write that file.
	if !fs.can("edit-files") {
		return nil, sftp.ErrSshFxPermissionDenied
	}

	p, err := fs.buildPath(request.Filepath)
	if err != nil {
		return nil, sftp.ErrSshFxNoSuchFile
	}

	fs.lock.Lock()
	defer fs.lock.Unlock()

	if _, err := os.Stat(p); os.IsNotExist(err) {
		return nil, sftp.ErrSshFxNoSuchFile
	}

	file, err := os.OpenFile(p, os.O_RDONLY, 0644)
	if err != nil {
		logger.Get().Errorw("could not open file for reading", zap.String("source", p), zap.Error(err))
		return nil, sftp.ErrSshFxFailure
	}

	return file, nil
}

// Handle a write action for a file on the system.
func (fs FileSystem) Filewrite(request *sftp.Request) (io.WriterAt, error) {
	if fs.ReadOnly {
		return nil, sftp.ErrSshFxOpUnsupported
	}

	p, err := fs.buildPath(request.Filepath)
	if err != nil {
		return nil, sftp.ErrSshFxNoSuchFile
	}

	// If the user doesn't have enough space left on the server it should respond with an
	// error since we won't be letting them write this file to the disk.
	if !fs.hasSpace() {
		logger.Get().Infow("denying file write due to space limit", zap.String("server", fs.UUID))
		return nil, sftp.ErrSshFxFailure
	}

	fs.lock.Lock()
	defer fs.lock.Unlock()

	_, statErr := os.Stat(p)
	// If the file doesn't exist we need to create it, as well as the directory pathway
	// leading up to where that file will be created.
	if os.IsNotExist(statErr) {
		// This is a different pathway than just editing an existing file. If it doesn't exist already
		// we need to determine if this user has permission to create files.
		if !fs.can("create-files") {
			return nil, sftp.ErrSshFxPermissionDenied
		}

		// Create all of the directories leading up to the location where this file is being created.
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			logger.Get().Errorw("error making path for file",
				zap.String("source", p),
				zap.String("path", filepath.Dir(p)),
				zap.Error(err),
			)
			return nil, sftp.ErrSshFxFailure
		}

		file, err := os.Create(p)
		if err != nil {
			logger.Get().Errorw("error creating file", zap.String("source", p), zap.Error(err))
			return nil, sftp.ErrSshFxFailure
		}

		// Not failing here is intentional. We still made the file, it is just owned incorrectly
		// and will likely cause some issues.
		if err := os.Chown(p, fs.User.Uid, fs.User.Gid); err != nil {
			logger.Get().Warnw("error chowning file", zap.String("file", p), zap.Error(err))
		}

		return file, nil
	}

	// If the stat error isn't about the file not existing, there is some other issue
	// at play and we need to go ahead and bail out of the process.
	if statErr != nil {
		logger.Get().Errorw("error performing file stat", zap.String("source", p), zap.Error(statErr))
		return nil, sftp.ErrSshFxFailure
	}

	// If we've made it here it means the file already exists and we don't need to do anything
	// fancy to handle it. Just pass over the request flags so the system knows what the end
	// goal with the file is going to be.
	//
	// But first, check that the user has permission to save modified files.
	if !fs.can("save-files") {
		return nil, sftp.ErrSshFxPermissionDenied
	}

	file, err := os.Create(p)
	if err != nil {
		logger.Get().Errorw("error writing to existing file",
			zap.Uint32("flags", request.Flags),
			zap.String("source", p),
			zap.Error(err),
		)
		return nil, sftp.ErrSshFxFailure
	}

	// Not failing here is intentional. We still made the file, it is just owned incorrectly
	// and will likely cause some issues.
	if err := os.Chown(p, fs.User.Uid, fs.User.Gid); err != nil {
		logger.Get().Warnw("error chowning file", zap.String("file", p), zap.Error(err))
	}

	return file, nil
}

// Hander for basic SFTP system calls related to files, but not anything to do with reading
// or writing to those files.
func (fs FileSystem) Filecmd(request *sftp.Request) error {
	if fs.ReadOnly {
		return sftp.ErrSshFxOpUnsupported
	}

	p, err := fs.buildPath(request.Filepath)
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
	case "Setstat":
		var mode os.FileMode = 0644
		if request.Attributes().FileMode().IsDir() {
			mode = 0755
		}

		if err := os.Chmod(p, mode); err != nil {
			logger.Get().Errorw("failed to perform setstat", zap.Error(err))
			return sftp.ErrSshFxFailure
		}
		return nil
	case "Rename":
		if !fs.can("move-files") {
			return sftp.ErrSshFxPermissionDenied
		}

		if err := os.Rename(p, target); err != nil {
			logger.Get().Errorw("failed to rename file",
				zap.String("source", p),
				zap.String("target", target),
				zap.Error(err),
			)
			return sftp.ErrSshFxFailure
		}

		break
	case "Rmdir":
		if !fs.can("delete-files") {
			return sftp.ErrSshFxPermissionDenied
		}

		if err := os.RemoveAll(p); err != nil {
			logger.Get().Errorw("failed to remove directory", zap.String("source", p), zap.Error(err))
			return sftp.ErrSshFxFailure
		}

		return sftp.ErrSshFxOk
	case "Mkdir":
		if !fs.can("create-files") {
			return sftp.ErrSshFxPermissionDenied
		}

		if err := os.MkdirAll(p, 0755); err != nil {
			logger.Get().Errorw("failed to create directory", zap.String("source", p), zap.Error(err))
			return sftp.ErrSshFxFailure
		}

		break
	case "Symlink":
		if !fs.can("create-files") {
			return sftp.ErrSshFxPermissionDenied
		}

		if err := os.Symlink(p, target); err != nil {
			logger.Get().Errorw("failed to create symlink",
				zap.String("source", p),
				zap.String("target", target),
				zap.Error(err),
			)
			return sftp.ErrSshFxFailure
		}

		break
	case "Remove":
		if !fs.can("delete-files") {
			return sftp.ErrSshFxPermissionDenied
		}

		if err := os.Remove(p); err != nil {
			logger.Get().Errorw("failed to remove a file", zap.String("source", p), zap.Error(err))
			return sftp.ErrSshFxFailure
		}

		return sftp.ErrSshFxOk
	default:
		return sftp.ErrSshFxOpUnsupported
	}

	var fileLocation = p
	if target != "" {
		fileLocation = target
	}

	// Not failing here is intentional. We still made the file, it is just owned incorrectly
	// and will likely cause some issues. There is no logical check for if the file was removed
	// because both of those cases (Rmdir, Remove) have an explicit return rather than break.
	if err := os.Chown(fileLocation, fs.User.Uid, fs.User.Gid); err != nil {
		logger.Get().Warnw("error chowning file", zap.String("file", fileLocation), zap.Error(err))
	}

	return sftp.ErrSshFxOk
}

// Handler for SFTP filesystem list calls. This will handle calls to list the contents of
// a directory as well as perform file/folder stat calls.
func (fs FileSystem) Filelist(request *sftp.Request) (sftp.ListerAt, error) {
	p, err := fs.buildPath(request.Filepath)
	if err != nil {
		return nil, sftp.ErrSshFxNoSuchFile
	}

	switch request.Method {
	case "List":
		if !fs.can("list-files") {
			return nil, sftp.ErrSshFxPermissionDenied
		}

		files, err := ioutil.ReadDir(p)
		if err != nil {
			logger.Get().Error("error listing directory", zap.Error(err))
			return nil, sftp.ErrSshFxFailure
		}

		return ListerAt(files), nil
	case "Stat":
		if !fs.can("list-files") {
			return nil, sftp.ErrSshFxPermissionDenied
		}

		s, err := os.Stat(p)
		if os.IsNotExist(err) {
			return nil, sftp.ErrSshFxNoSuchFile
		} else if err != nil {
			logger.Get().Error("error running STAT on file", zap.Error(err))
			return nil, sftp.ErrSshFxFailure
		}

		return ListerAt([]os.FileInfo{s}), nil
	default:
		// Before adding readlink support we need to evaluate any potential security risks
		// as a result of navigating around to a location that is outside the home directory
		// for the logged in user. I don't forsee it being much of a problem, but I do want to
		// check it out before slapping some code here. Until then, we'll just return an
		// unsupported response code.
		return nil, sftp.ErrSshFxOpUnsupported
	}
}

// Normalizes a directory we get from the SFTP request to ensure the user is not able to escape
// from their data directory. After normalization if the directory is still within their home
// path it is returned. If they managed to "escape" an error will be returned.
func (fs FileSystem) buildPath(rawPath string) (string, error) {
	// Calling filepath.Clean on the joined directory will resolve it to the absolute path,
	// removing any ../ type of path resolution, and leaving us with the absolute final path.
	p := filepath.Clean(filepath.Join(fs.Directory, rawPath))

	// If the new path doesn't start with their root directory there is clearly an escape
	// attempt going on, and we should NOT resolve this path for them.
	if !strings.HasPrefix(p, fs.Directory) {
		return "", errors.New("invalid path resolution")
	}

	return p, nil
}

// Determines if a user has permission to perform a specific action on the SFTP server. These
// permissions are defined and returned by the Panel API.
func (fs FileSystem) can(permission string) bool {
	// Server owners and super admins have their permissions returned as '[*]' via the Panel
	// API, so for the sake of speed do an initial check for that before iterating over the
	// entire array of permissions.
	if len(fs.Permissions) == 1 && fs.Permissions[0] == "*" {
		return true
	}

	// Not the owner or an admin, loop over the permissions that were returned to determine
	// if they have the passed permission.
	for _, p := range fs.Permissions {
		if p == permission {
			return true
		}
	}

	return false
}

// Determines if the directory a file is trying to be added to has enough space available
// for the file to be written to.
//
// Because determining the amount of space being used by a server is a taxing operation we
// will load it all up into a cache and pull from that as long as the key is not expired.
func (fs FileSystem) hasSpace() bool {
	// This is a safety measure to ensure that users who encounter FS related issues can
	// quickly disable this feature to allow me time to look into what is going wrong and
	// hopefully address it.
	//
	// I get the feeling this might be used sooner rather than later unfortunately...
	if fs.DisableDiskCheck {
		return true
	}

	var space int64 = -2
	if x, exists := fs.Cache.Get("disk:" + fs.UUID); exists {
		space = x.(int64)
	}

	// If the value is still -2 it means we didn't manage to grab anything out of the cache.
	// In that case, read the server configuration and then plop that value into the cache.
	// If there is an error reading the configuration just return true, can't do anything more
	// until the error gets resolved.
	if space == -2 {
		b, err := ioutil.ReadFile(fs.ServerConfig)
		if err != nil {
			logger.Get().Errorf(
				"error reading server configuration, cannot determine disk limit",
				zap.String("server", fs.UUID),
				zap.Error(err),
			)
			return true
		}

		s, err := jsonparser.GetInt(b, "build", "disk")
		if err != nil {
			s = 0
		}

		fs.Cache.Set("disk:"+fs.UUID, s, cache.DefaultExpiration)
		space = s
	}

	// If space is -1 or 0 just return true, means they're allowed unlimited.
	if space <= 0 {
		logger.Get().Debugw("server marked as not having space limit", zap.String("server", fs.UUID))
		return true
	}

	var size int64
	if x, exists := fs.Cache.Get("used:" + fs.UUID); exists {
		size = x.(int64)
	}

	// If there is no size its either because there is no data (in which case running this function
	// will have effectively no impact), or there is nothing in the cache, in which case we need to
	// grab the size of their data directory. This is a taxing operation, so we want to store it in
	// the cache once we've gotten it.
	if size == 0 {
		size = fs.directorySize(fs.Directory)
		logger.Get().Debugw("got directory size from taxing operation", zap.String("server", fs.UUID), zap.Int64("size", size))
		fs.Cache.Set("used:"+fs.UUID, size, cache.DefaultExpiration)
	}

	// Determine if their folder size, in bytes, is smaller than the amount of space they've
	// been allocated.
	return (size / 1024.0 / 1024.0) <= space
}

// Determines the directory size of a given location by running parallel tasks to iterate
// through all of the folders. Returns the size in bytes.
func (fs FileSystem) directorySize(dir string) int64 {
	var size int64
	var wg sync.WaitGroup

	files, err := ioutil.ReadDir(dir)
	if err != nil {
		logger.Get().Errorw("error reading directory", zap.String("directory", dir), zap.Error(err))
		return 0
	}

	for _, f := range files {
		if f.IsDir() {
			wg.Add(1)
			go func(p string) {
				defer wg.Done()
				size += fs.directorySize(p)
			}(path.Join(dir, f.Name()))
		} else {
			size += f.Size()
		}
	}

	wg.Wait()

	return size
}