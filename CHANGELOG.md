# Changelog
This file is a running track of new features and fixes to each version of the daemon released starting with `v1.0.3`.

## v1.0.3
### Fixed
* Fixes a regression in file permission handling via SFTP. File permissions can now be changed and are not forced to a specific setting.
* **[Security]** Fixes an unauthorized file read outside of server directory vulnerability when working with the standalone SFTP server.