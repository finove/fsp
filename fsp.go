/*
Package fsp provides a interface for connect fsp server

	package main
	import (
		"github.com/finove/fsp"
		"fmt"
	)

	func main() {
		var err error
		var fspSession *fsp.Session
		fspSession, err = fsp.NewSession("127.0.0.1:21", "123456")
		if err != nil {
			return
		}
		defer fspSession.Close()
		fmt.Printf("fsp server version: %s\n", fspSession.Version())
	}

*/
package fsp

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	// VERSION represent fsp lib version.
	VERSION = "1.0.1"
)

// An Error represents a fsp error
type Error interface {
	error
	Timeout() bool
}

// NewSession return a new Session
func NewSession(serverAddress, password string) (session *Session, err error) {
	var conn *net.UDPConn
	conn, err = net.ListenUDP("udp4", nil)
	if err != nil {
		return
	}
	session, err = NewSessionWithConn(conn, serverAddress, password)
	return
}

// NewSessionWithConn return a new Session
func NewSessionWithConn(conn *net.UDPConn, serverAddress, password string) (session *Session, err error) {
	var addr *net.UDPAddr
	if conn == nil || serverAddress == "" {
		err = newOpError("invalid conn or server address")
		return
	}
	addr, err = net.ResolveUDPAddr("udp4", serverAddress)
	if err != nil {
		return
	}
	if addr.Port == 0 {
		err = newOpError("invalid server port")
		return
	}
	session = &Session{
		serverAddr: addr,
		conn:       conn,
		password:   password,
	}
	session.loadKey()
	session.setDefault()
	session.verbose(0, "connect from %s to %s", session.conn.LocalAddr().String(), session.serverAddr.String())
	return
}

// Close close fsp session
func (s *Session) Close() {
	var bye fspPacket
	s.saveKey()
	if s.conn == nil {
		return
	}
	// send bye
	bye.cmd = FSPCommandBye
	bye.len = 0
	bye.xlen = 0
	bye.pos = 0
	s.transaction(&bye)
	s.conn.Close()
	s.conn = nil
}

// Version Get server version string and setup
func (s *Session) Version() (version string) {
	var pkt fspPacket
	pkt.cmd = FSPCommandVersion
	pkt.xlen = 0
	pkt.pos = 0
	resp, err := s.transaction(&pkt)
	if err != nil {
		return
	}
	version = string(resp.buf[:resp.len])
	return
}

// Readdir reads the contents of the directory
func (s *Session) Readdir(dirpath string) (fi []os.FileInfo, err error) {
	var di *dir
	var entrys []*dirEntry
	var entry *dirEntry
	di, err = s.getDir(dirpath)
	if err != nil || di == nil {
		return
	}
	entrys = di.ListEntrys()
	for _, entry = range entrys {
		var st fileStat
		st.name = entry.Name
		st.modTime = time.Unix(entry.LastModify, 0)
		st.size = int64(entry.Size)
		if entry.Type == fspEntryTypeDir {
			st.mode = os.ModeDir | 0755
		} else {
			st.mode = 0644
		}
	}
	return
}

// ShowDir display the files of the dir
func (s *Session) ShowDir(dirpath string) (err error) {
	var di *dir
	var numFiles, numDirs, numLinks int
	var entrys []*dirEntry
	var entry *dirEntry
	di, err = s.getDir(dirpath)
	if err != nil || di == nil {
		return
	}
	entrys = di.ListEntrys()
	fmt.Printf("[start]\n")
	for _, entry = range entrys {
		if entry.Name == "." || entry.Name == ".." {
			continue
		}
		if entry.Type == fspEntryTypeFile {
			numFiles++
		} else if entry.Type == fspEntryTypeDir {
			numDirs++
		} else if entry.Type == fspEntryTypeLink {
			numLinks++
		}
		fmt.Printf("%s\n", entry.Show())
	}
	fmt.Printf("[end]\n")
	fmt.Printf("file-%d, link-%d, dir-%d\n", numFiles, numLinks, numDirs)
	return
}

// DwonloadFile download file from fsp server
func (s *Session) DwonloadFile(remotePath, savePath string, retry int) (err error) {
	var stat os.FileInfo
	stat, err = s.Stat(remotePath)
	if err != nil {
		return
	}
	s.startDownload(stat.Size())
	err = s.getFile(remotePath, savePath, retry)
	s.finishDownload()
	return
}

// DownloadDirectory download dir from fsp server
func (s *Session) DownloadDirectory(remotePath, savePath string) (err error) {
	var di *dir
	var totalSize, totalCount int
	var saveDir, getFile, saveFile, tmpSaveFile string
	var entrys []*dirEntry
	var finfo os.FileInfo
	if savePath == "" {
		if remotePath[0] == '/' {
			saveDir = remotePath[1:]
		} else {
			saveDir = remotePath
		}
	} else {
		saveDir = savePath
	}
	di, err = s.getDir(remotePath)
	if err != nil {
		return
	}
	entrys = di.ListEntrys()
	for _, entry := range entrys {
		if entry.Type == fspEntryTypeFile {
			totalCount++
			totalSize += int(entry.Size)
		}
	}
	s.startDownload(int64(totalSize))
	for _, entry := range entrys {
		if entry.Type != fspEntryTypeFile {
			continue
		}
		getFile = filepath.Join(remotePath, entry.Name)
		saveFile = filepath.Join(saveDir, entry.Name)
		tmpSaveFile = saveFile + ".tmp"
		if finfo, err = os.Stat(saveFile); err == nil && finfo.Size() == int64(entry.Size) {
			s.verbose(0, "file %s already download", saveFile)
			s.trans.updateUnit(0, int64(entry.Size))
			continue
		}
		err = s.getFile(getFile, tmpSaveFile, 3)
		if err != nil {
			s.verbose(0, "file %s download fail, %v", getFile, err)
			break
		}
		err = os.Rename(tmpSaveFile, saveFile)
		if err != nil {
			s.verbose(0, "rename file %s to %s fail, %v", tmpSaveFile, saveFile, err)
		} else {
			fmt.Printf("get file %s done\n", entry.Name)
		}
	}
	s.finishDownload()
	return
}

// Mkdir create a directory
func (s *Session) Mkdir(directory string) (err error) {
	return s.simpleCommand(directory, FSPCommandMakeDir)
}

// RemoveAll delete a directory
func (s *Session) RemoveAll(path string) (err error) {
	return s.simpleCommand(path, FSPCommandDelDir)
}

// Remove delete a file
func (s *Session) Remove(name string) (err error) {
	return s.simpleCommand(name, FSPCommandDelFile)
}

// Rename rename file
func (s *Session) Rename(oldpath, newpath string) (err error) {
	var out fspPacket
	err = out.buildFileName(oldpath, s.password)
	if err != nil {
		return
	}
	if (len(newpath) + int(out.len)) > FSPSpace {
		err = newOpError("file name too long")
		return
	}
	copy(out.buf[out.len:], newpath[:])
	out.xlen = uint16(len(newpath))
	if s.password != "" {
		if out.len+out.xlen > FSPSpace {
			err = newOpError("file name too long")
			return
		}
		out.buf = append(out.buf, '\n')
		out.xlen += uint16(1)
		copy(out.buf[out.len+out.xlen:], s.password[:])
		out.xlen += uint16(len(s.password))
	}
	out.buf = append(out.buf, 0)
	out.xlen += uint16(1)
	out.cmd = FSPCommandRename
	out.pos = uint32(out.xlen)
	_, err = s.transaction(&out)
	return
}

// UploadFile upload file to fsp server
func (s *Session) UploadFile(localFile, remotePath string) (err error) {
	var fp *os.File
	var fspFile *File
	var fileName = filepath.Base(localFile)
	var buff []byte
	var done int
	if len(remotePath) == 0 {
		remotePath = fileName
	} else if os.IsPathSeparator(remotePath[len(remotePath)-1]) {
		remotePath = filepath.Join(remotePath, fileName)
	}
	s.verbose(0, "start upload %s to %s\n", localFile, remotePath)
	fp, err = os.Open(localFile)
	if err != nil || fp == nil {
		err = newOpError("local file not exist")
		return
	}
	defer fp.Close()
	fspFile, err = s.openFile(remotePath, "w")
	if err != nil {
		return
	}
	defer fspFile.Close()
	buff = make([]byte, 1024)
	for {
		done, err = fp.Read(buff)
		if err != nil {
			if err.Error() == "EOF" {
				err = nil
			} else {
				err = newOpError(err.Error())
			}
			break
		}
		if done > 0 {
			fspFile.Write(buff[:done])
		} else {
			break
		}
	}
	return
}

// GetProtecion get protection byte from directory
func (s *Session) GetProtecion(directory string) (protection uint8, err error) {
	var out fspPacket
	var resp fspPacket
	err = out.buildFileName(directory, s.password)
	if err != nil {
		return
	}
	out.cmd = FSPCommandGetPro
	out.xlen = 0
	out.pos = 0

	resp, err = s.transaction(&out)
	if err != nil {
		return
	}
	if resp.pos != FSPProBytes {
		err = newOpError("GetProtecion ENOMSG")
		return
	}
	protection = resp.buf[resp.len]
	return
}

// Stat get information about file/directory
func (s *Session) Stat(name string) (info os.FileInfo, err error) {
	var st fileStat
	var out fspPacket
	var resp fspPacket
	err = out.buildFileName(name, s.password)
	if err != nil {
		return
	}
	out.cmd = FSPCommandStat
	out.xlen = 0
	out.pos = 0
	resp, err = s.transaction(&out)
	if err != nil {
		return
	}
	if len(resp.buf) <= 8 || resp.buf[8] == 0 {
		err = newOpError("No such file")
		return
	}
	var modTime = binary.BigEndian.Uint32(resp.buf[:4])
	st.name = name
	st.modTime = time.Unix(int64(modTime), 0)
	st.size = int64(binary.BigEndian.Uint32(resp.buf[4:]))
	if resp.buf[8] == fspEntryTypeDir {
		st.mode = os.ModeDir | 0755
	} else {
		st.mode = 0644
	}
	return st, nil
}

// CanUpload check is user has enough privs for uploading the file
func (s *Session) CanUpload(fileName string) (err error) {
	var protection uint8
	var dirName = filepath.Dir(fileName)
	protection, err = s.GetProtecion(dirName)
	if err != nil {
		return
	}
	if protection&FSPDirOwner > 0 {
		return
	}
	if protection&FSPDirAdd == 0 {
		err = newOpError("files cann't be added to this dir")
		return
	}
	if protection&FSPDirDel > 0 {
		return
	}
	_, err = s.Stat(fileName)
	if err == nil {
		err = newOpError("file exist already")
	} else {
		err = nil
	}
	return
}

// ChangePassword change password of fsp server
func (s *Session) ChangePassword(newPassword string) (err error) {
	var req fspPacket
	// date format like: dir name\nold passwd\nnew passwd
	req.buf = []byte("\n")
	req.len = 1
	if len(s.password) > 0 {
		req.buf = append(req.buf, []byte(s.password)...)
		req.len += uint16(len(s.password))
	}
	req.buf = append(req.buf, '\n')
	req.len++
	if len(newPassword) > 0 {
		req.buf = append(req.buf, []byte(newPassword)...)
		req.len += uint16(len(newPassword))
	}
	// add terminating \0
	req.buf = append(req.buf, 0)
	req.len++

	req.cmd = FSPCommandChangePass
	req.xlen = 0
	req.pos = 0
	_, err = s.transaction(&req)
	return
}
