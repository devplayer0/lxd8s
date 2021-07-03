.PHONY: all clean

SOURCES = $(shell find internal/ cmd/ -name '*.go')

all: bin/livenessd bin/vmmd

bin/%: $(SOURCES)
	go build -o $@ ./cmd/$(shell basename $@)

clean:
	-rm -rf bin
