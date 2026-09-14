.PHONY: all host cudahost build test test-cuda clean

all: host cudahost build

host: cl/host.cpp cl/kernel.cl
	g++ -std=c++11 -O3 -o host -Icl cl/host.cpp -ldl -lpthread

cudahost: cuda/host.cpp cuda/kernel.cu
	g++ -std=c++11 -O3 -o cuda_host -Icuda cuda/host.cpp -ldl -lpthread

build:
	go build -o gpuminer .

test:
	go test ./...

# Fake CUDA driver / fake NVRTC test backend for CI without a GPU (see CUDA_TESTS.md).
test-cuda:
	gcc -Wall -Wextra -std=c11 -o cuda/test/api_tests \
		cuda/test/api_tests.c cuda/test/cpu_reference.c \
		cuda/test/fake_cuda.c cuda/test/fake_nvrtc.c -pthread
	./cuda/test/api_tests
	gcc -Wall -Wextra -std=c11 -fPIC -shared -o /tmp/cu_fake_libcuda.so.1 \
		cuda/test/fake_cuda.c cuda/test/cpu_reference.c -pthread
	gcc -Wall -Wextra -std=c11 -fPIC -shared -o /tmp/cu_fake_libnvrtc.so.12 \
		cuda/test/fake_nvrtc.c
	go test -run 'TestCudaBackend' -v .

clean:
	rm -f host cuda_host gpuminer cuda/test/api_tests