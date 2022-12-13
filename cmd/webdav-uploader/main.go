package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"github.com/studio-b12/gowebdav"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

func main() {
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	cfg := initCmd()
	if cfg == nil {
		return
	}
	client := &webDavClient{
		baseUrl: cfg.WebDavUrl,
		user:    cfg.User,
		pass:    cfg.Password,
	}
	err := client.New().Connect()
	if err != nil {
		fmt.Println("can not connect to webdav", err)
		return
	}

	err = uploadToDav(cfg.LocalPath, cfg.RemotePath, client)
	if err != nil {
		fmt.Println("can not upload to webdav", err)
		return
	}
}

func initCmd() *Config {
	cfg := &Config{}
	flag.StringVar(&cfg.LocalPath, "local", "", "local path")
	flag.StringVar(&cfg.RemotePath, "remote", "", "remote path")
	flag.StringVar(&cfg.WebDavUrl, "url", "", "webdav url")
	flag.StringVar(&cfg.User, "user", "", "user")
	flag.StringVar(&cfg.Password, "pwd", "", "password")
	flag.Parse()
	if cfg.LocalPath == "" {
		fmt.Println("local path is empty")
		return nil
	}
	if cfg.RemotePath == "" {
		fmt.Println("remote path is empty")
		return nil
	}
	if cfg.WebDavUrl == "" {
		fmt.Println("webdav url is empty")
		return nil
	}
	if cfg.User == "" {
		fmt.Println("user is empty")
		return nil
	}
	if cfg.Password == "" {
		fmt.Println("password is empty")
		return nil
	}
	return cfg
}

type Config struct {
	WebDavUrl  string
	User       string
	Password   string
	LocalPath  string
	RemotePath string
}

func uploadToDav(localPath string, remotePath string, client *webDavClient) error {
	localPath = filepath.Clean(localPath)

	stat, err := os.Stat(localPath)
	if err != nil {
		return err
	}

	if stat.IsDir() {
		return filepath.Walk(localPath, func(p string, info fs.FileInfo, err error) error {
			if p[len(localPath):] == "" {
				return nil
			}
			relativePath := p[len(localPath)+1:]
			if localPath == "." {
				relativePath = p
			}

			if info.IsDir() {
				err := makeDir(client, urlPathJoin(remotePath, relativePath))
				if err != nil {
					return err
				}
			} else {
				err := uploadFile(client, p, urlPathJoin(remotePath, relativePath))
				if err != nil {
					return err
				}
			}
			return nil
		})
	} else {
		fileName := localPath[len(filepath.Dir(localPath))+1:]
		return uploadFile(client, localPath, urlPathJoin(remotePath, fileName))
	}
}

func urlPathJoin(p ...string) string {
	s := filepath.Join(p...)
	if runtime.GOOS == "windows" {
		return strings.ReplaceAll(s, "\\", "/")
	} else {
		return s
	}
}

func makeDir(client *webDavClient, remotePath string) error {
	c := client.New()
	err := c.Mkdir(remotePath, 0644)
	if err != nil {
		return err
	}
	return nil
}

func uploadFile(client *webDavClient, p string, remotePath string) error {
	file, err := os.Open(p)
	if err != nil {
		return err
	}
	defer file.Close()

	readerChan := make(chan fileChunk)
	var wg sync.WaitGroup

	go func() {
		err := readFile(file, readerChan)
		close(readerChan)
		if err != nil {
			fmt.Printf("read file filed, err: %v\n", err)
		}
	}()

	for i := 0; i < 16; i++ {
		wg.Add(1)
		go uploadFileSplit(&wg, readerChan, client, remotePath)
	}

	wg.Wait()
	return nil
}

func readFile(f *os.File, readerChan chan fileChunk) error {
	fi, err := f.Stat()
	if err != nil {
		return err
	}

	maxReadSize := int64(64 * 1024 * 1024)

	// read file to channel
	for i := int64(0); ; i++ {
		offset := i * maxReadSize
		if offset >= fi.Size() {
			break
		}
		readLen := maxReadSize
		needRead := fi.Size() - offset
		if needRead < maxReadSize {
			readLen = needRead
		}
		readerChan <- fileChunk{
			Offset: offset,
			Length: readLen,
			Size:   fi.Size(),
			Reader: io.NewSectionReader(f, offset, readLen),
		}
	}
	return nil
}

func uploadFileSplit(wg *sync.WaitGroup, readerChan chan fileChunk, client *webDavClient, remotePath string) {
	defer wg.Done()
	for chunk := range readerChan {
		fmt.Printf("chunk: %d %d %d\n", chunk.Offset, chunk.Offset+chunk.Length, chunk.Size)
		c := client.New()
		c.SetHeader("Content-Range", fmt.Sprintf("bytes %d-%d/%d", chunk.Offset, chunk.Offset+chunk.Length-1, chunk.Size))
		err := c.WriteStream(remotePath, chunk.Reader, 0644)
		if err != nil {
			fmt.Printf("upload file split failed, err: %v\n", err)
		}
	}
}

type fileChunk struct {
	Offset int64
	Length int64
	Size   int64
	Reader io.Reader
}

type webDavClient struct {
	baseUrl string
	user    string
	pass    string
}

func (w *webDavClient) New() *gowebdav.Client {
	return gowebdav.NewClient(w.baseUrl, w.user, w.pass)
}
