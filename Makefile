GITTAG := `git describe --tags`
VERSION := `git describe --abbrev=0 --tags`
RELEASE := `git rev-list $(shell git describe --abbrev=0 --tags).. --count`
DESTDIR?=dist
BUILD_TIME := `date +%FT%T%z`
# Setup the -ldflags option for go build here, interpolate the variable values
LDFLAGS := -ldflags "-X main.GitTag=${GITTAG} -X main.Version=${VERSION} -X main.Build=${BUILD_TIME}"

.PHONY: binary
binary: clean build

.PHONY: clean
clean:
	rm -rf *.deb
	rm -rf *.rpm
	rm -rf godns
	rm -rf ${DESTDIR} 

.PHONY: build

build:
	go build -o godns  ${LDFLAGS}

amd64:
	GOOS=linux GOARCH=amd64 go build -o godns  ${LDFLAGS}

.PHONY: pkg
pkg:
	rm -rf ${DESTDIR}
	mkdir ${DESTDIR}
	mkdir -p ${DESTDIR}/usr/local/bin
	mkdir -p ${DESTDIR}/etc/
	cp godns ${DESTDIR}/usr/local/bin/
	cp -r logrotate.d ${DESTDIR}/etc/
	mkdir -p ${DESTDIR}/etc/godns/
	cp godns.conf  ${DESTDIR}/etc/godns/dns.conf
	mkdir -p ${DESTDIR}/etc/init.d/
	cp init.d/godns ${DESTDIR}/etc/init.d/godns

deb: clean amd64 pkg
	fpm -t deb -s dir -n godns --rpm-os linux -v $(VERSION:v%=%) --config-files /etc/godns --iteration ${RELEASE} -C ${DESTDIR}

rpm: clean amd64 pkg
	fpm -t rpm -s dir -n godns --rpm-os linux -v ${VERSION} --config-files /etc/godns --iteration ${RELEASE} -C ${DESTDIR}
