package server

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/buger/jsonparser"
	"github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
	"github.com/pkg/sftp"
	"github.com/pterodactyl/sftp-server/src/logger"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

type AuthenticationRequest struct {
	User string `json:"username"`
	Pass string `json:"password"`
}

type Settings struct {
	BasePath         string
	ReadOnly         bool
	BindPort         int
	BindAddress      string
	ServerDataFolder string
	DisableDiskCheck bool
}

type Configuration struct {
	Data     []byte
	Cache    *cache.Cache
	Settings Settings
}

type AuthenticationResponse struct {
	Server      string   `json:"server"`
	Token       string   `json:"token"`
	Permissions []string `json:"permissions"`
}

// Initalize the SFTP server and add a persistent listener to handle inbound SFTP connections.
func (c Configuration) Initalize() error {
	serverConfig := &ssh.ServerConfig{
		NoClientAuth: false,
		MaxAuthTries: 6,
		PasswordCallback: func(conn ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			sp, err := c.validateCredentials(conn.User(), pass)
			if err != nil {
				return nil, errors.New("could not validate credentials")
			}

			return sp, nil
		},
	}

	_, err := os.Stat(path.Join(c.Settings.BasePath, ".sftp/id_rsa"))
	if os.IsNotExist(err) {
		logger.Get().Info("creating new private key for server")
		if err := c.generatePrivateKey(); err != nil {
			return err
		}
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

	logger.Get().Infow("server listener registered", zap.String("address", listener.Addr().String()))

	for {
		conn, _ := listener.Accept()
		if conn != nil {
			go c.AcceptInboundConnection(conn, serverConfig)
		}
	}

	return nil
}

// Handles an inbound connection to the instance and determines if we should serve the request
// or not.
func (c Configuration) AcceptInboundConnection(conn net.Conn, config *ssh.ServerConfig) {
	defer conn.Close()

	// Before beginning a handshake must be performed on the incoming net.Conn
	sconn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		logger.Get().Warnw("failed to accept an incoming connection", zap.Error(err))
		return
	}
	defer sconn.Close()

	logger.Get().Debugw("accepted inbound connection",
		zap.String("ip", conn.RemoteAddr().String()),
		zap.String("user", sconn.Permissions.Extensions["user"]),
		zap.String("uuid", sconn.Permissions.Extensions["uuid"]),
	)

	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		// If its not a session channel we just move on because its not something we
		// know how to handle at this point.
		if newChannel.ChannelType() != "session" {
			logger.Get().Debugw("received an unknown channel type", zap.String("channel", newChannel.ChannelType()))
			newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			logger.Get().Warnw("could not accept a channel", zap.Error(err))
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
			logger.Get().Errorw("got a server connection with no uuid")
			continue
		}

		// Create a new handler for the currently logged in user's server.
		fs := c.createHandler(sconn.Permissions)

		// Create the server instance for the channel using the filesystem we created above.
		server := sftp.NewRequestServer(channel, fs)

		if err := server.Serve(); err == io.EOF {
			server.Close()
		} else if err != nil {
			logger.Get().Errorw("sftp server closed with error", zap.Error(err))
		}
	}
}

// Creates a new SFTP handler for a given server. The directory argument should
// be the base directory for a server. All actions done on the server will be
// relative to that directory, and the user will not be able to escape out of it.
func (c Configuration) createHandler(perm *ssh.Permissions) sftp.Handlers {
	base, err := jsonparser.GetString(c.Data, "sftp", "path")
	if err != nil || base == "" {
		base = "/srv/daemon-data"
	}

	p := FileSystem{
		ServerConfig:     path.Join(c.Settings.ServerDataFolder, perm.Extensions["uuid"], "server.json"),
		Directory:        path.Join(base, perm.Extensions["uuid"]),
		UUID:             perm.Extensions["uuid"],
		Permissions:      strings.Split(perm.Extensions["permissions"], ","),
		ReadOnly:         c.Settings.ReadOnly,
		Cache:            c.Cache,
		DisableDiskCheck: c.Settings.DisableDiskCheck,
	}

	return sftp.Handlers{
		FileGet:  p,
		FilePut:  p,
		FileCmd:  p,
		FileList: p,
	}
}

// Validates a set of credentials for a SFTP login aganist Pterodactyl Panel and returns
// the server's UUID if the credentials were valid.
func (c Configuration) validateCredentials(user string, pass []byte) (*ssh.Permissions, error) {
	data, _ := json.Marshal(AuthenticationRequest{User: user, Pass: string(pass)})

	url, err := jsonparser.GetString(c.Data, "remote", "base")
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/remote/sftp", url), bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}

	token, err := jsonparser.GetString(c.Data, "keys", "[0]")
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/vnd.pterodactyl.v1+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s, _ := ioutil.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("bad credentials provided: %s", string(s))
		}

		if resp.StatusCode == http.StatusBadRequest {
			return nil, fmt.Errorf("server in bad state, SFTP denied, %s", string(s))
		}

		return nil, fmt.Errorf("error response from server: %s", string(s))
	}

	j := &AuthenticationResponse{}
	json.NewDecoder(resp.Body).Decode(j)

	p := &ssh.Permissions{}
	p.Extensions = make(map[string]string)
	p.Extensions["uuid"] = j.Server
	p.Extensions["user"] = user
	p.Extensions["permissions"] = strings.Join(j.Permissions, ",")

	return p, nil
}

// Generates a private key that will be used by the SFTP server.
func (c Configuration) generatePrivateKey() error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(path.Join(c.Settings.BasePath, ".sftp"), 0755); err != nil {
		return err
	}

	o, err := os.Create(path.Join(c.Settings.BasePath, ".sftp/id_rsa"))
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
