package sftp_server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"github.com/patrickmn/go-cache"
	"github.com/pkg/sftp"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"io"
	"io/ioutil"
	"net"
	"os"
	"path"
	"strings"
	"time"
)

type Settings struct {
	BasePath         string
	ReadOnly         bool
	BindPort         int
	BindAddress      string
	ServerDataFolder string
	DisableDiskCheck bool
}

type SftpUser struct {
	Uid int
	Gid int
}

type Server struct {
	// A custom logger instance that should be used by the server.
	logger *zap.SugaredLogger
	cache  *cache.Cache

	Settings Settings
	User     SftpUser

	PathValidator      func(fs FileSystem, p string) (string, error)
	DiskSpaceValidator func(fs FileSystem) bool

	// Validator function that is called when a user connects to the server. This should
	// check against whatever system is desired to confirm if the given username and password
	// combination is valid. If so, should return an authentication response.
	CredentialValidator func(r AuthenticationRequest) (*AuthenticationResponse, error)
}

// Create a new server configuration instance.
func New(c *Server) error {
	if logger, err := zap.NewProduction(); err != nil {
		return err
	} else {
		c.logger = logger.Sugar()
	}

	c.cache = cache.New(5*time.Minute, 10*time.Minute)

	return nil
}

// Allows configuration of a custom logger.
func (c *Server) ConfigureLogger(cb func() *zap.SugaredLogger) {
	c.logger = cb()
}

// Initalize the SFTP server and add a persistent listener to handle inbound SFTP connections.
func (c *Server) Initalize() error {
	serverConfig := &ssh.ServerConfig{
		NoClientAuth: false,
		MaxAuthTries: 6,
		PasswordCallback: func(conn ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			resp, err := c.CredentialValidator(AuthenticationRequest{
				User: conn.User(),
				Pass: string(pass),
			})

			if err != nil {
				return nil, err
			}

			sshPerm := &ssh.Permissions{
				Extensions: map[string]string{
					"uuid":        resp.Server,
					"user":        conn.User(),
					"permissions": strings.Join(resp.Permissions, ","),
				},
			}

			return sshPerm, nil
		},
	}

	if _, err := os.Stat(path.Join(c.Settings.BasePath, ".sftp/id_rsa")); os.IsNotExist(err) {
		if err := c.generatePrivateKey(); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	privateBytes, err := ioutil.ReadFile(path.Join(c.Settings.BasePath, ".sftp/id_rsa"))
	if err != nil {
		return err
	}

	private, err := ssh.ParsePrivateKey(privateBytes)
	if err != nil {
		return err
	}

	// Add our private key to the server configuration.
	serverConfig.AddHostKey(private)

	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", c.Settings.BindAddress, c.Settings.BindPort))
	if err != nil {
		return err
	}

	c.logger.Infow("sftp subsystem listening for connections", zap.String("host", c.Settings.BindAddress), zap.Int("port", c.Settings.BindPort))

	for {
		conn, _ := listener.Accept()
		if conn != nil {
			go c.AcceptInboundConnection(conn, serverConfig)
		}
	}
}

// Handles an inbound connection to the instance and determines if we should serve the request
// or not.
func (c Server) AcceptInboundConnection(conn net.Conn, config *ssh.ServerConfig) {
	defer conn.Close()

	// Before beginning a handshake must be performed on the incoming net.Conn
	sconn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer sconn.Close()

	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		// If its not a session channel we just move on because its not something we
		// know how to handle at this point.
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}

		// Channels have a type that is dependent on the protocol. For SFTP this is "subsystem"
		// with a payload that (should) be "sftp". Discard anything else we receive ("pty", "shell", etc)
		go func(in <-chan *ssh.Request) {
			for req := range in {
				ok := false

				switch req.Type {
				case "subsystem":
					if string(req.Payload[4:]) == "sftp" {
						ok = true
					}
				}

				req.Reply(ok, nil)
			}
		}(requests)

		// Configure the user's home folder for the rest of the request cycle.
		if sconn.Permissions.Extensions["uuid"] == "" {
			continue
		}

		// Create a new handler for the currently logged in user's server.
		fs := c.createHandler(sconn.Permissions)

		// Create the server instance for the channel using the filesystem we created above.
		server := sftp.NewRequestServer(channel, fs)

		if err := server.Serve(); err == io.EOF {
			server.Close()
		}
	}
}

// Creates a new SFTP handler for a given server. The directory argument should
// be the base directory for a server. All actions done on the server will be
// relative to that directory, and the user will not be able to escape out of it.
func (c Server) createHandler(perm *ssh.Permissions) sftp.Handlers {
	p := FileSystem{
		UUID:             perm.Extensions["uuid"],
		Permissions:      strings.Split(perm.Extensions["permissions"], ","),
		ReadOnly:         c.Settings.ReadOnly,
		Cache:            c.cache,
		DisableDiskCheck: c.Settings.DisableDiskCheck,
		User:             c.User,
		HasDiskSpace:     c.DiskSpaceValidator,
		PathValidator:    c.PathValidator,
		logger:           c.logger,
	}

	return sftp.Handlers{
		FileGet:  p,
		FilePut:  p,
		FileCmd:  p,
		FileList: p,
	}
}

// Generates a private key that will be used by the SFTP server.
func (c Server) generatePrivateKey() error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(path.Join(c.Settings.BasePath, ".sftp"), 0755); err != nil {
		return err
	}

	o, err := os.OpenFile(path.Join(c.Settings.BasePath, ".sftp/id_rsa"), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer o.Close()

	pkey := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}

	if err := pem.Encode(o, pkey); err != nil {
		return err
	}

	return nil
}
