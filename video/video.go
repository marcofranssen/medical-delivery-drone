package video

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"

	"gocv.io/x/gocv"
)

const (
	frameX     = 960
	frameY     = 720
	bufferSize = frameX * frameY * 3
)

// Converter converts raw drone video data using ffmpeg
type Converter struct {
	ffmpeg     *exec.Cmd
	fileWriter *bytes.Buffer
	writer     io.WriteCloser
	reader     io.ReadCloser
}

// NewConverter creates a new instance of Converter
func NewConverter(writeRawToFile bool) (*Converter, error) {
	ffmpeg := exec.Command("ffmpeg", "-hwaccel", "auto", "-hwaccel_device", "opencl", "-i", "pipe:0",
		"-pix_fmt", "bgr24", "-s", strconv.Itoa(frameX)+"x"+strconv.Itoa(frameY), "-f", "rawvideo", "pipe:1")
	ffmpegIn, err := ffmpeg.StdinPipe()
	if err != nil {
		return nil, err
	}
	ffmpegOut, err := ffmpeg.StdoutPipe()
	if err != nil {
		return nil, err
	}

	var fileWriter *bytes.Buffer = nil
	if writeRawToFile {
		fileWriter = bytes.NewBuffer(make([]byte, 0))
	}

	return &Converter{
		ffmpeg:     ffmpeg,
		fileWriter: fileWriter,
		writer:     ffmpegIn,
		reader:     ffmpegOut,
	}, nil
}

// Start starts the conversion by reading from piped data
func (c Converter) Start(ctx context.Context) (<-chan []byte, error) {
	err := c.ffmpeg.Start()
	if err != nil {
		return nil, err
	}
	videoChan := make(chan []byte)
	go pipeToChan(ctx, c, videoChan)
	return videoChan, nil
}

// Write writes data to pipe
func (c Converter) Write(pkt []byte) (int, error) {
	if c.fileWriter != nil {
		n, err := c.fileWriter.Write(pkt)
		if err != nil {
			return n, err
		}
	}

	return c.writer.Write(pkt)
}

// Read reads data from pipe
func (c Converter) Read(pkt []byte) (int, error) {
	return c.reader.Read(pkt)
}

func (c Converter) writeToFile() error {
	if c.fileWriter != nil {
		err := os.MkdirAll("fixtures", 0755)
		if err != nil {
			return err
		}

		file, err := os.OpenFile("fixtures/video-stream.dat", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			return err
		}
		defer file.Close()

		if err := gob.NewEncoder(file).Encode(c.fileWriter.Bytes()); err != nil {
			return err
		}
	}

	return nil
}

// Close closes the reader and writer
func (c Converter) Close() error {
	c.writeToFile()

	errW := c.writer.Close()
	errR := c.reader.Close()

	if errW != nil && errR != nil {
		return fmt.Errorf("failed to close writer and reader")
	}
	if errW != nil {
		return fmt.Errorf("failed to close writer")
	}
	if errR != nil {
		return fmt.Errorf("failed to close reader")
	}
	return nil
}

// Display displays the video using gocv by consuming the channel
func Display(ctx context.Context, videoChan <-chan []byte) error {
	// OpenCV window to watch the live video stream from Tello.
	window := gocv.NewWindow("Tello")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case buf, ok := <-videoChan:
			if !ok {
				return fmt.Errorf("channel closed")
			}
			img, _ := gocv.NewMatFromBytes(frameY, frameX, gocv.MatTypeCV8UC3, buf)
			if img.Empty() {
				return nil
			}

			window.IMShow(img)
			if window.WaitKey(1) >= 0 {
				return errors.New("window wait more than 1")
			}
		}
	}
}

func pipeToChan(ctx context.Context, reader io.ReadCloser, videoChan chan<- []byte) {
	defer close(videoChan)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			buf := make([]byte, bufferSize)
			if _, err := io.ReadFull(reader, buf); err != nil {
				log.Println("failed reading into buffer", err)
			}
			videoChan <- buf
		}
	}
}
