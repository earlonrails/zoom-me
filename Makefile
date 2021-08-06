.PHONY: install run

install:
	cd src && go build

run: install
	cd src && ./zoom-me
