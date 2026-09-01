.PHONY: all build host clean

all: host build

host: cl/host.cpp cl/kernel.cl
	g++ -std=c++11 -O3 -o host -Icl cl/host.cpp -ldl -lpthread

build:
	go build -o gpuminer .

clean:
	rm -f host gpuminer