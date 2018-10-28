package server

import (
	"fmt"
	"github.com/buger/jsonparser"
	"github.com/pkg/sftp"
	"github.com/pterodactyl/sftp-server/src/logger"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"io"
	"io/ioutil"
	"net"
	"path"
)

type Settings struct {
	BasePath string
	Debug    bool
	ReadOnly bool
}

type Configuration struct {
	Data     []byte
	Settings Settings
}

func (c Configuration) Initalize() error {
	port, _ := jsonparser.GetString(c.Data, "sftp", "port")
	if port == "" {
		port = "2022"
	}

	ip, _ := jsonparser.GetString(c.Data, "sftp", "ip")
	if ip == "" {
		ip = "0.0.0.0"
	}

	serverConfig := &ssh.ServerConfig{
		NoClientAuth: false,
		MaxAuthTries: 6,
		PasswordCallback: func(conn ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			logger.Get().Debugw("received connection to SFTP server", zap.String("user", conn.User()))

			if conn.User() == "dane" && string(pass) == "test" {
				return nil, nil
			}

			return nil, fmt.Errorf("password rejected for %q", conn.User())
		},
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

	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%s", ip, string(port)))
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
	_, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		logger.Get().Error("failed to accept an incoming connection", zap.Error(err))
	}

	logger.Get().Debugw("accepted inbound connection", zap.String("ip", conn.RemoteAddr().String()))

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

		// Create a new SFTP filesystem handler. This is currently hard-coded because I'm lazy and
		// haven't yet gotten around to actually hitting the API and getting the expected results back.
		fs := CreateHandler("/Users/dane/Downloads")

		// Create the server instance for the channel using the filesystem we created above.
		server := sftp.NewRequestServer(channel, fs)

		if err := server.Serve(); err == io.EOF {
			server.Close()
		} else if err != nil {
			logger.Get().Errorw("sftp server closed with error", zap.Error(err))
		}
	}
}
