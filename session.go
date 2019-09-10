package fsp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// An Error represents a fsp error
type Error interface {
	error
	Timeout() bool
}

// Stat file stat info
type Stat struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
}

// Size length in bytes for regular file
func (s Stat) Size() int64 {
	return s.size
}

// Name base name of the file
func (s Stat) Name() string {
	return filepath.Base(s.name)
}

// Mode file mode bits
func (s Stat) Mode() os.FileMode {
	return s.mode
}

// ModTime modification time
func (s Stat) ModTime() time.Time {
	return s.modTime
}

// IsDir abbreviation for Mode().IsDir()
func (s Stat) IsDir() bool {
	return s.mode.IsDir()
}

// Sys underlying data source (can return nil)
func (s Stat) Sys() interface{} {
	return nil
}

// OpError is the error type usually returned by functions in the fsp package
type OpError struct {
	Cmd    uint8  // FSP command operator
	Reason string // FSP command fail reason
	Err    error
}

func newOpError(errStr string) (err *OpError) {
	err = &OpError{
		Err: errors.New(errStr),
	}
	return
}

// Timeout check is time out error
func (e *OpError) Timeout() bool {
	if e == nil {
		return false
	}
	if strings.Contains(e.Reason, "time out") {
		return true
	}
	if e.Err != nil && strings.Contains(e.Err.Error(), "timeout") {
		return true
	}
	return false
}

func (e *OpError) Error() string {
	var s string
	if e == nil {
		return "<nil>"
	}
	if e.Reason != "" {
		s = e.Reason
	} else {
		s = e.Err.Error()
	}
	return s
}

type transUnit struct {
	startTime time.Time
	endTime   time.Time
	doneSize  int64
	count     int
}

func (t *transUnit) Reset() {
	t.startTime = time.Now()
	t.endTime = time.Now()
	t.doneSize = 0
	t.count = 0
}

func (t *transUnit) Speed() (speed float64) {
	var milli time.Duration
	if t.doneSize > 0 {
		milli = time.Since(t.startTime) / time.Millisecond
		speed = float64(t.doneSize) / float64(milli) * 1000.0 / 1024
	}
	return
}

type transferControl struct {
	initial     bool
	pktSize     uint16
	startTime   time.Time
	endTime     time.Time
	curr        transUnit
	doneSize    int64
	totalSize   int64
	avgSpeed    int64
	circleTime  time.Duration
	circleCount int
}

func (t *transferControl) Reset() {
	t.startTime = time.Now()
	t.curr.Reset()
	t.doneSize = 0
	t.totalSize = 0
	t.circleCount = 100
	t.circleTime = 10 * time.Second
	t.pktSize = 768
	t.initial = true
}

func (t *transferControl) Percent() (percent int64) {
	if t.totalSize > 0 {
		percent = t.doneSize * 100 / t.totalSize
	}
	return
}

func (t *transferControl) ShowSpeed() {
	log.Printf("total-%d KB,done-%d KB(%d%%),speed-%.2f KB/s,packet_size-%d byte", t.totalSize/1024, t.doneSize/1024,
		t.Percent(), t.curr.Speed(), t.pktSize)
}

func (t *transferControl) updateUnit(retry uint16, rcevLen int64) {
	t.doneSize += rcevLen
	t.curr.doneSize += rcevLen
	t.curr.count++
	if time.Since(t.curr.endTime) >= time.Second {
		t.ShowSpeed()
		t.curr.endTime = time.Now()
	}
}

// Session fsp session
type Session struct {
	conn       *net.UDPConn
	serverAddr *net.UDPAddr
	password   string
	lock       string
	lockFile   string
	timeOut    uint
	maxDelay   uint
	trans      transferControl
	seq        uint16 // sequence number
	dupes      uint   // total pkt. dupes
	resends    uint   // total pkt. sends
	trips      uint   // total pkt. trips
	rtts       uint32 // cumul. rtt
	verboseLvl int    // verbose level
}

// StartDownload set total download size
func (s *Session) StartDownload(total int64) {
	s.trans.Reset()
	s.trans.totalSize = total
}

// FinishDownload complete transfer
func (s *Session) FinishDownload() {
	s.trans.ShowSpeed()
}

// transaction make one send + receive transaction with server
func (s *Session) transaction(pkt *fspPacket) (resp fspPacket, err error) {
	var retry uint16
	var firstSend = time.Now()
	var delay = time.Duration(1340) * time.Millisecond
	pkt.key = s.clientGetKey()
	retry = s.randUint16() & 0xfff8
	if s.seq == retry {
		s.seq ^= 0x1080
	} else {
		s.seq = retry
	}
	retry = 0
	for ; ; retry++ {
		if time.Since(firstSend) > time.Duration(s.timeOut)*time.Second {
			err = &OpError{Err: errors.New("transaction timeout")}
			break
		}
		pkt.seq = s.seq | (retry & 0x7)
		err = pkt.write(s)
		if err != nil {
			time.Sleep(time.Second)
			delay += time.Second
			retry--
			continue
		}
		if retry <= 0 {
			delay = time.Duration(1340) * time.Millisecond
		} else {
			delay = delay * 3 / 2
		}
		for {
			var n int
			var buff []byte
			buff = make([]byte, FSPMaxPacket)
			s.conn.SetReadDeadline(time.Now().Add(delay))
			n, err = s.conn.Read(buff)
			if err != nil || n <= 0 {
				s.clientSetKey(pkt.key)
				break
			}
			err = resp.read(buff[:n])
			if err != nil {
				s.verbose(0, "read respone fail, %s, %v", string(buff[:n]), err)
				continue
			}
			if resp.seq&0xfff8 != s.seq {
				s.dupes++
				continue
			}
			if resp.cmd != pkt.cmd && resp.cmd != FSPCommandErr {
				s.dupes++
				continue
			}
			// check correct filepos
			if resp.pos != pkt.pos && (pkt.cmd == FSPCommandGetDir || pkt.cmd == FSPCommandGetFile ||
				pkt.cmd == FSPCommandUpload || pkt.cmd == FSPCommandGrabFile || pkt.cmd == FSPCommandInfo) {
				s.dupes++
				continue
			}
			if resp.cmd == FSPCommandErr {
				// fmt.Printf("Failed, code=%d,reason=\"%s\"\n", FSPCommandErr, resp.buf)
				err = &OpError{Cmd: resp.cmd, Reason: string(resp.buf)}
			} else if resp.cmd == FSPCommandGetFile || resp.cmd == FSPCommandGetFile2 {
				s.trans.updateUnit(retry, int64(resp.len))
			}
			s.clientSetKey(resp.key)
			return
		}
	}
	return
}

// simpleCommand simple FSP command
func (s *Session) simpleCommand(directory string, command uint8) (err error) {
	var out fspPacket
	err = out.buildFileName(directory, s.password)
	if err != nil {
		return
	}
	out.cmd = command
	out.xlen = 0
	out.pos = 0
	_, err = s.transaction(&out)
	if err != nil {
		return
	}
	return
}

func (s *Session) setDefault() {
	s.timeOut = 10
	s.maxDelay = 2
	s.seq = s.randUint16() & 0xfff8
}

func (s *Session) verbose(level int, format string, v ...interface{}) {
	if s.verboseLvl >= level {
		log.Printf(format, v...)
	}
}

func (s *Session) loadKey() {
	s.lockFile = filepath.Join(os.TempDir(), fmt.Sprintf("FSP%s", "1"))
	buff, err := ioutil.ReadFile(s.lockFile)
	if err != nil {
		s.lock = "13579"
		s.verbose(1, "loadKey fail %v", err)
		return
	}
	s.lock = string(buff)
	s.verbose(1, "loadKey %s from %s", s.lock, s.lockFile)
	return
}

func (s *Session) saveKey() {
	ioutil.WriteFile(s.lockFile, []byte(s.lock), os.ModePerm)
}

func (s *Session) clientGetKey() (key uint16) {
	v, _ := strconv.Atoi(s.lock)
	key = uint16(v)
	return
}

func (s *Session) clientSetKey(key uint16) {
	s.lock = fmt.Sprintf("%d", key)
}

func (s *Session) randUint16() uint16 {
	return uint16(rand.Intn(65535))
}

// getDir get directory list
/*
	request
	file position:  position in directory
	data:           ASCIIZ directory name
	xtra data:	(not required)
				word - preferred size of directory block

	reply
	file position:  same as in request
	data:           directory listing (format follows)
	xtra data:	not used
*/
func (s *Session) getDir(dirName string) (dir *Dir, err error) {
	var p fspPacket
	var pos uint32
	var resp fspPacket
	var tmpBuff = make([]byte, 2)
	if dirName == "" {
		dirName = "/"
	}
	err = p.buildFileName(dirName, s.password)
	if err != nil {
		return
	}
	dir = &Dir{}
	p.cmd = FSPCommandGetDir
	binary.BigEndian.PutUint16(tmpBuff, s.trans.pktSize)
	p.buf = append(p.buf, tmpBuff...)
	p.xlen = 2
	for {
		p.pos = pos
		resp, err = s.transaction(&p)
		if err != nil {
			dir.data = make([]byte, 0)
			break
		}
		if resp.len == 0 {
			break
		}
		if dir.blockSize == 0 {
			dir.blockSize = resp.len
		}
		dir.data = append(dir.data, resp.buf...)
		pos += uint32(resp.len)
		if resp.len < dir.blockSize {
			break
		}
	}
	if err != nil {
		return
	}
	if len(dir.data) > 0 {
		dir.inUse = 1
		dir.dirName = dirName
		dir.dataSize = uint(pos)
	} else {
		err = &OpError{Err: fmt.Errorf("read dir %s fail", dirName)}
		dir = nil
	}
	return
}

// openFile open fsp file
func (s *Session) openFile(remoteFile, mode string) (fspFile *File, err error) {
	if remoteFile == "" || mode == "" {
		err = &OpError{Err: errors.New("invald param for open fsp file")}
		return
	}
	fspFile = &File{
		writing: false,
		s:       s,
		name:    remoteFile,
	}
	switch mode[0] {
	case 'r':
	case 'w':
		fspFile.writing = true
	case 'a':
		fallthrough
	default:
		fspFile = nil
		err = &OpError{Err: errors.New("not support")}
		return
	}
	if mode[1:] == "+" || mode[1:] == "b+" {
		fspFile = nil
		err = &OpError{Err: errors.New("not support")}
		return
	}
	fspFile.out.xlen = 0
	if fspFile.writing {
		fspFile.out.cmd = FSPCommandUpload
	} else {
		err = fspFile.out.buildFileName(remoteFile, s.password)
		if err != nil {
			fspFile = nil
			return
		}
		fspFile.out.cmd = FSPCommandGetFile
		fspFile.out.xlen = 2
		fspFile.buffPos = FSPSpace
	}
	return
}

// getFile download file from fsp server
func (s *Session) getFile(remotePath, savePath string, retry int) (err error) {
	var fp *os.File
	var fileName = filepath.Base(remotePath)
	var saveFile string
	var fspFile *File
	var buff []byte
	var done int
	if savePath == "" {
		saveFile = fileName
	} else if len(savePath) > 0 && os.IsPathSeparator(savePath[len(savePath)-1]) {
		saveFile = filepath.Join(savePath, fileName)
	} else {
		saveFile = savePath
	}
	err = os.MkdirAll(filepath.Dir(saveFile), os.ModePerm)
	if err != nil {
		err = &OpError{Err: fmt.Errorf("create save directory fail, %v", err)}
		s.verbose(1, "create save directory fail, %v", err)
		return
	}
TRYAGAIN:
	fp, err = os.Create(saveFile)
	if err != nil {
		err = &OpError{Err: fmt.Errorf("create file %s fail, %v", saveFile, err)}
		s.verbose(1, "create file %s fail, %v", saveFile, err)
		return
	}
	defer fp.Close()
	fspFile, err = s.openFile(remotePath, "rb")
	if err != nil || fspFile == nil {
		s.verbose(1, "open fsp file fail, err %v", err)
		return
	}
	buff = make([]byte, FSPSpace)
	for {
		done, err = fspFile.Read(buff, 1, 1024)
		if err != nil {
			break
		}
		if done > 0 {
			fp.Write(buff[:done])
		} else {
			break
		}
	}
	fspFile.Close()
	if op, ok := err.(Error); retry > 0 && ok && op.Timeout() == true {
		retry--
		goto TRYAGAIN
	}
	return
}
