# TFTPD

This is a modern re-implementation of [tftpd](https://linux.die.net/man/8/tftpd) (trivial file transfer protocol) server.

## Implementation

This implementaion of tftpd is fully self contained, stand-alone, statically linked binary, with zero dependencies. Doesn't require `inetd` or anything else. It can be run as a Docker container with ease.

## Usage

### Server

```sh
./tftpd -root_dir=/some/path
```

The server must bind to port `69/udp`, which may require elevated privileges.

### Docker

https://hub.docker.com/r/tenox7/tftpd

Inside docker container root dir is `/srv`:

```sh
docker run -d --name tftpd -v /some/dir:/srv -p 69:69 tenox7/tftpd:latest
```

## Legal

- This code has been writen entirely by Claude
- License: Public Domain
