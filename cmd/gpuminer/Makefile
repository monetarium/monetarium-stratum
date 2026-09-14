.PHONY: all host cudahost build clean

all: host cudahost build

host: cl/host.cpp cl/kernel.cl
	g++ -std=c++11 -O3 -o host -Icl cl/host.cpp -ldl -lpthread

cudahost: cuda/host.cpp cuda/kernel.cu
	g++ -std=c++11 -O3 -o cuda_host -Icuda cuda/host.cpp -ldl -lpthread

build:
	go build -o gpuminer .

clean:
	rm -f host cuda_host gpuminer