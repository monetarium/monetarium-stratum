package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"

	"github.com/decred/slog"
)

// workMessage is a work item sent to the GPU host.
type workMessage struct {
	Type   string `json:"type"`
	Header string `json:"header"`
	Target string `json:"target"`
}

// solutionMessage is a found nonce reported by the GPU host.
type solutionMessage struct {
	Type          string `json:"type"`
	Nonce         uint32 `json:"nonce"`
	Header        string `json:"header"`
	NoncesChecked uint64 `json:"nonces_checked"`
}

// progressMessage is periodic progress reported by the GPU host.
type progressMessage struct {
	Type          string `json:"type"`
	NoncesChecked uint64 `json:"nonces_checked"`
}

// searchedMessage reports that a full 2^32 nonce sweep completed with no
// solution.
type searchedMessage struct {
	Type          string `json:"type"`
	NoncesChecked uint64 `json:"nonces_checked"`
}

// gpuHost manages the OpenCL host subprocess.
type gpuHost struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	mu        sync.Mutex
	solutions chan solutionMessage
	progress  chan progressMessage
	searched  chan searchedMessage
	done      chan struct{}
}

// startGpuHost launches the host binary and wires its output channels.
func startGpuHost(hostPath, kernelDir string, deviceIdx int, log slog.Logger) (*gpuHost, error) {
	cmd := exec.Command(hostPath, kernelDir, strconv.Itoa(deviceIdx))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return nil, fmt.Errorf("start host: %w", err)
	}

	h := &gpuHost{
		cmd:       cmd,
		stdin:     stdin,
		solutions: make(chan solutionMessage, 1),
		progress:  make(chan progressMessage, 16),
		searched:  make(chan searchedMessage, 1),
		done:      make(chan struct{}),
	}
	go h.readLoop(stdout)
	return h, nil
}

// send writes a JSON message to the host.
func (h *gpuHost) send(msg interface{}) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return json.NewEncoder(h.stdin).Encode(msg)
}

// stop closes the host's stdin and waits for it to exit.
func (h *gpuHost) stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stdin != nil {
		h.stdin.Close()
		h.stdin = nil
	}
	h.cmd.Wait()
	<-h.done
}

// readLoop reads host output lines and dispatches them to the channels.
func (h *gpuHost) readLoop(stdout io.Reader) {
	defer close(h.done)
	reader := bufio.NewReader(stdout)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "host read error: %v\n", err)
			}
			return
		}
		var base struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &base); err != nil {
			continue
		}
		switch base.Type {
		case "solution":
			var msg solutionMessage
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			h.solutions <- msg
		case "progress":
			var msg progressMessage
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			select {
			case h.progress <- msg:
			default:
			}
		case "searched":
			var msg searchedMessage
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			h.searched <- msg
		}
	}
}
