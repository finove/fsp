package fsp

import (
	"encoding/binary"
	"fmt"
)

// definition of FSP protocol v2 commands
const (
	FSPCommandVersion    = 0x10 /* return server's version string.      */
	FSPCommandInfo       = 0x11 /* return server's extended info block  */
	FSPCommandErr        = 0x40 /* error response from server.          */
	FSPCommandGetDir     = 0x41 /* get a directory listing.             */
	FSPCommandGetFile    = 0x42 /* get a file.                          */
	FSPCommandUpload     = 0x43 /* open a file for writing.             */
	FSPCommandInstall    = 0x44 /* close a file opened for writing.     */
	FSPCommandDelFile    = 0x45 /* delete a file.                       */
	FSPCommandDelDir     = 0x46 /* delete a directory.                  */
	FSPCommandGetPro     = 0x47 /* get directory protection.            */
	FSPCommandSetPro     = 0x48 /* set directory protection.            */
	FSPCommandMakeDir    = 0x49 /* create a directory.                  */
	FSPCommandBye        = 0x4A /* finish a session.                    */
	FSPCommandGrabFile   = 0x4B /* atomic get+delete a file.            */
	FSPCommandGrabDone   = 0x4C /* atomic get+delete a file done.       */
	FSPCommandStat       = 0x4D /* get information about file.          */
	FSPCommandRename     = 0x4E /* rename file or directory.            */
	FSPCommandChangePass = 0x4F /* change password                      */
	FSPCommandLimit      = 0x80 /* # > 0x7f for future cntrl blk ext.   */
	FSPCommandTest       = 0x81 /* reserved for testing                 */
)

// FSP packet
const (
	FSPHSzie     = 12
	FSPSpace     = 14708
	FSPMaxPacket = FSPHSzie + FSPSpace
)

// byte offsets of fields in the FSP v2 header
const (
	fspOffsetCmd = 0
	fspOffsetSum = 1
	fspOffsetKey = 2
	fspOffsetSeq = 4
	fspOffsetLen = 6
	fspOffsetPos = 8
)

// directory protection bits
const (
	FSPProBytes  = 1
	FSPDirOwner  = 0x01 // caller owns the directory
	FSPDirDel    = 0x02 // files can be deleted from this dir
	FSPDirAdd    = 0x04 // files can be added to this dir
	FSPDirMkDir  = 0x08 // new subdirectories can be created
	FSPDirGet    = 0x10 // files are NOT readable by non-owners
	FSPDirReadme = 0x20 // directory contain an readme file
	FSPDirList   = 0x40 // directory can be listed
	FSPDirRename = 0x80 // files can be renamed in this directory
)

type fspPacket struct {
	cmd  uint8  // FSP_COMMAND
	sum  uint8  // MESSAGE_CHECKSUM
	key  uint16 // message KEY
	seq  uint16 // message SEQUENCE
	len  uint16 // DATA_LENGTH
	pos  uint32 // FILE_POSITION
	xlen uint16 // number of bytes in buf2
	buf  []byte // packet payload
}

// read 解析收到的FSP包
func (pkt *fspPacket) read(buff []byte) (err error) {
	var mySum int
	if len(buff) < FSPHSzie {
		err = newOpError("recv packet too short")
		return
	}
	if len(buff) > FSPMaxPacket {
		err = newOpError("recv packet too long")
		return
	}
	for _, b := range buff {
		mySum += int(b)
	}
	mySum -= int(buff[fspOffsetSum])
	mySum = (mySum + (mySum >> 8)) & 0xff
	if mySum != int(buff[fspOffsetSum]) {
		err = newOpError(fmt.Sprintf("checksum fail, mySum %x, got %x", mySum, buff[fspOffsetSum]))
		return
	}
	pkt.cmd = buff[fspOffsetCmd]
	pkt.sum = uint8(mySum)
	pkt.key = binary.BigEndian.Uint16(buff[fspOffsetKey:])
	pkt.seq = binary.BigEndian.Uint16(buff[fspOffsetSeq:])
	pkt.len = binary.BigEndian.Uint16(buff[fspOffsetLen:])
	pkt.pos = binary.BigEndian.Uint32(buff[fspOffsetPos:])
	if (int(pkt.len) + FSPHSzie) > len(buff) {
		err = newOpError("fsp packet length field invalid")
		return
	}
	pkt.xlen = uint16(len(buff)) - pkt.len - FSPHSzie
	pkt.buf = make([]byte, len(buff)-FSPHSzie)
	copy(pkt.buf, buff[FSPHSzie:])
	return
}

// buildFileName set fileName and password
func (pkt *fspPacket) buildFileName(fileName, password string) (err error) {
	if (len(fileName) + len(password) + 2) >= FSPSpace {
		err = newOpError("file name too long")
		return
	}
	pkt.buf = append(pkt.buf, fileName[:]...)
	pkt.len = uint16(len(fileName))
	if password != "" {
		pkt.buf = append(pkt.buf, '\n')
		pkt.len++
		pkt.buf = append(pkt.buf, password[:]...)
		pkt.len += uint16(len(password))
	}
	pkt.buf = append(pkt.buf, 0)
	pkt.len++
	return
}

func (pkt *fspPacket) write(s *Session) (err error) {
	var checksum = 0
	var used int
	var sendBuff = make([]byte, FSPHSzie)
	if pkt.xlen+pkt.len > FSPSpace {
		err = newOpError("packet payload too big")
		return
	}
	sendBuff[fspOffsetCmd] = pkt.cmd
	sendBuff[fspOffsetSum] = 0
	binary.BigEndian.PutUint16(sendBuff[fspOffsetKey:], pkt.key)
	binary.BigEndian.PutUint16(sendBuff[fspOffsetSeq:], pkt.seq)
	binary.BigEndian.PutUint16(sendBuff[fspOffsetLen:], pkt.len)
	binary.BigEndian.PutUint32(sendBuff[fspOffsetPos:], pkt.pos)
	used = FSPHSzie
	sendBuff = append(sendBuff, pkt.buf[:pkt.len]...)
	used += int(pkt.len)

	if pkt.cmd == FSPCommandGetFile {
		// for dynamically adjusting the pkt size to adjust speed of transction
		if pkt.xlen == 2 {
			sendBuff = append(sendBuff, []byte{0, 0}...)
			binary.BigEndian.PutUint16(sendBuff[used:], s.trans.pktSize)
			used += int(pkt.xlen)
		}
	} else if pkt.xlen > 0 {
		sendBuff = append(sendBuff, pkt.buf[pkt.len:pkt.len+pkt.xlen]...)
		used += int(pkt.xlen)
	}
	for _, b := range sendBuff[:used] {
		checksum += int(b)
	}
	checksum += used
	sendBuff[fspOffsetSum] = uint8(checksum + (checksum >> 8))
	_, err = s.conn.WriteToUDP(sendBuff[:used], s.serverAddr)
	if err != nil {
		err = newOpError(err.Error())
	}
	return
}
