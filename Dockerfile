FROM scratch
ARG TARGETARCH
ADD tftpd-${TARGETARCH}-linux /tftpd
ENTRYPOINT ["/tftpd","-root_dir", "/srv"]
EXPOSE 69
LABEL maintainer="as@tenoware.com"
