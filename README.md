# Pterodactyl SFTP Server
This package serves as a SFTP server to run alongside the [Pterodactyl Daemon](https://github.com/pterodactyl/daemon).
It is not designed to run stand-alone on your system.

## Why?
Previously we made use of Nodejs to handle running our SFTP server for the Daemon. While this worked, and has allowed us
to provide more advanced permissions and keep things off the host OS, it has also lead to many issues. Most notably has
been extremely poor performance when trying to work with directories containing hundreds or thousands of files. In addition,
this poor performance has also caused bottlenecking in the application when it comes to managing servers.

Our goal with the introduction of this standalone server is to provide a lean, performant, fully-featured SFTP server
that will hold up the the demands placed upon it.

## Running
To run this program in a standalone mode (rather than booted by the Daemon), use the arguments below.

```
./sftp-server [--config-path] [--port] [--bind-addr] [--readonly] [--disable-disk-check] [--debug]
```

### Flags
```
flag          default            help
--config-path       ./config/core.json This flag defines the location of the Daemon configuration file which
                                       is read to help determine some internal settings.

--port               2022              The port for the SFTP server.

--bind-addr          0.0.0.0           The bind address for the SFTP server.

--readonly           false             If provided, the server will run in a read-only state that denies anyone,
                                       regardless of permission, from writing or modifying files.

--disable-disk-check false             If passed, disk space usage will be disabled.

--debug              false             If passed, the server will run in debug mode which can be useful for printing
                                       stacktraces and additional connection and error information.
```

## License
Like all of our software, this server is provided under the MIT license.

```
Copyright (c) 2018 Dane Everitt <dane@daneeveritt.com>

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```