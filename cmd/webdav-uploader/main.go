package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"

	"github.com/studio-b12/gowebdav"
)

func main() {
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	cfg := initCmd()
	if cfg == nil {
		return
	}
	c, err := connectWebdav(cfg.WebDavUrl, cfg.User, cfg.Password)
	if err != nil {
		fmt.Println(err)
		return
	}
	err = uploadToDav(cfg.LocalPath, c, cfg.RemotePath)
	if err != nil {
		fmt.Println(err)
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

func uploadToDav(localPath string, c *gowebdav.Client, remotePath string) error {
	localPath = path.Clean(localPath)

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
				err := makeDir(c, path.Join(remotePath, relativePath))
				if err != nil {
					return err
				}
			} else {
				err := uploadFile(p, c, path.Join(remotePath, relativePath))
				if err != nil {
					return err
				}
			}
			return nil
		})
	} else {
		fileName := localPath[len(path.Dir(localPath))+1:]
		return uploadFile(localPath, c, path.Join(remotePath, fileName))
	}
}

func makeDir(c *gowebdav.Client, remotePath string) error {
	err := c.Mkdir(remotePath, 0644)
	if err != nil {
		return err
	}
	return nil
}

func uploadFile(p string, c *gowebdav.Client, remotePath string) error {
	file, err := os.Open(p)
	defer file.Close()
	if err != nil {
		return err
	}
	err = c.WriteStream(remotePath, file, 0644)
	if err != nil {
		return err
	}
	return nil
}

func connectWebdav(webDavUrl, user, password string) (*gowebdav.Client, error) {
	c := gowebdav.NewClient(webDavUrl, user, password)
	err := c.Connect()
	if err != nil {
		return nil, err
	}
	return c, nil
}
