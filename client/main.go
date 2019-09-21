package main

import (
	"fmt"
	"github.com/finove/fsp"
	"github.com/urfave/cli"
	"log"
	"net"
	"os"
	"sort"
	"time"
)

var (
	serverIP                       string
	localPort, remotePort          uint
	serverPass, serverNewPass      string
	cmdLS, cmdGet, cmdSave, cmdPut string
	showServerVersion              bool
	showClientVersion              bool
)

func main() {
	var app = cli.NewApp()
	app.Name = "fspclient"
	app.Version = "0.0.1"
	app.Usage = "fsp client"
	app.Description = "download file using fsp protocol"
	app.Compiled, _ = time.Parse("2006-01-02 15:04:05", "2019-09-10 15:20:00")
	app.Authors = []cli.Author{
		cli.Author{
			Name:  "finove",
			Email: "finove@qq.com",
		},
	}
	app.Copyright = "Copyright 2019 (c)"
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:        "ip",
			Value:       "",
			Usage:       "fsp server ip:port",
			Destination: &serverIP,
		},
		cli.UintFlag{
			Name:        "port",
			Value:       0,
			Usage:       "local port for used",
			Destination: &localPort,
		},
		cli.UintFlag{
			Name:        "dport",
			Value:       0,
			Usage:       "fsp server port",
			Destination: &remotePort,
		},
		cli.StringFlag{
			Name:        "p",
			Value:       "",
			Usage:       "fsp server password",
			Destination: &serverPass,
		},
		cli.StringFlag{
			Name:        "np",
			Value:       "",
			Usage:       "change the password of FSP server",
			Destination: &serverNewPass,
		},
		cli.StringFlag{
			Name:        "put",
			Value:       "",
			Usage:       "upload file path",
			Destination: &cmdPut,
		},
		cli.StringFlag{
			Name:        "ls",
			Value:       "",
			Usage:       "fsp command list files",
			Destination: &cmdLS,
		},
		cli.StringFlag{
			Name:        "g",
			Value:       "",
			Usage:       "fsp command get files",
			Destination: &cmdGet,
		},
		cli.StringFlag{
			Name:        "s",
			Value:       "",
			Usage:       "get file save path",
			Destination: &cmdSave,
		},
		cli.BoolFlag{
			Name:        "server_version",
			Usage:       "show server version",
			Destination: &showServerVersion,
		},
	}

	app.Action = func(c *cli.Context) (err error) {
		var fspSession *fsp.Session
		var addr *net.UDPAddr
		var conn *net.UDPConn
		addr, conn, err = getFSPServerIP()
		if err != nil {
			log.Printf("Failed, get fsp server ip %v", err)
			return
		}
		fspSession, err = fsp.NewSessionWithConn(conn, addr.String(), serverPass)
		if err != nil {
			log.Printf("Failed, open fsp session %v", err)
			return
		}
		if showServerVersion {
			fmt.Printf("fsp server version: %s\n", fspSession.Version())
		} else if cmdLS != "" {
			err = fspSession.ShowDir(cmdLS)
			if err != nil {
				log.Printf("Failed, read dir %v", err)
			}
		} else if cmdGet != "" {
			if len(cmdGet) > 0 && os.IsPathSeparator(cmdGet[len(cmdGet)-1]) {
				err = fspSession.DownloadDirectory(cmdGet, cmdSave)
			} else {
				err = fspSession.DwonloadFile(cmdGet, cmdSave, 3)
			}
			if err != nil {
				log.Printf("Failed, get file %s error %v", cmdGet, err)
			}
		} else if serverNewPass != "" {
			err = fspSession.ChangePassword(serverNewPass)
			if err != nil {
				log.Printf("Failed, change password error %v", err)
			}
		} else if cmdPut != "" {
			err = fspSession.UploadFile(cmdPut, cmdSave)
			if err != nil {
				log.Printf("Failed, upload file error %v", err)
			}
		}
		fspSession.Close()
		return
	}

	sort.Sort(cli.FlagsByName(app.Flags))
	sort.Sort(cli.CommandsByName(app.Commands))
	app.Run(os.Args)
}

func getFSPServerIP() (addr *net.UDPAddr, conn *net.UDPConn, err error) {
	var localAddr *net.UDPAddr
	if localPort > 0 {
		localAddr, _ = net.ResolveUDPAddr("udp4", fmt.Sprintf(":%d", localPort))
	}
	if serverIP != "" {
		addr, err = net.ResolveUDPAddr("udp4", serverIP)
		if err != nil {
			return nil, nil, err
		}
		conn, err = net.ListenUDP("udp4", localAddr)
	} else {
		err = fmt.Errorf("miss command line parameter, need -ip or -id or -mac value")
		return nil, nil, err
	}
	return
}
