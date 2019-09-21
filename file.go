package fsp

import (
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// types of directory entry
const (
	fspEntryTypeEnd  = 0x00
	fspEntryTypeFile = 0x01
	fspEntryTypeDir  = 0x02
	fspEntryTypeLink = 0x03
	fspEntryTypeSkip = 0x2A
)

// dirEntry fsp directory entry info
type dirEntry struct {
	Name       string
	NameLen    uint16
	Type       uint8
	RecordLen  uint16
	Size       uint
	LastModify int64
}

// Show display entry
func (entry *dirEntry) Show() (resp string) {
	var bb strings.Builder
	var modify time.Time
	switch entry.Type {
	case fspEntryTypeDir:
		bb.WriteString("dir    ")
	case fspEntryTypeFile:
		bb.WriteString("file   ")
	case fspEntryTypeLink:
		bb.WriteString("link   ")
	default:
		return
	}
	modify = time.Unix(entry.LastModify, 0)
	bb.WriteString(fmt.Sprintf("%10d %s %s", entry.Size, modify.Format("2006/01/02 15:04:05"), entry.Name))
	resp = bb.String()
	return
}

// dir fsp dir cache
type dir struct {
	dirName   string
	inUse     uint16
	dirPos    int
	blockSize uint16
	dataSize  uint
	data      []byte
}

// ListEntrys get all dir entrys
func (d *dir) ListEntrys() (entrys []*dirEntry) {
	var err error
	var entry *dirEntry
	for {
		if d.dirPos < 0 || d.dirPos%4 != 0 {
			// RDIRENT is followed by enough number of padding to fill to an 4-byte boundary.
			break
		}
		entry, err = d.ReadNative()
		if err != nil || entry == nil {
			break
		}
		entrys = append(entrys, entry)
	}
	return
}

// ReadNative get dir entry form dir
/*
struct RDIRENT {
	struct HEADER {
		long  time;
		long  size;
		byte  type;
	}
	ASCIIZ name;
}
*/
func (d *dir) ReadNative() (entry *dirEntry, err error) {
	var fType byte
	var nameLen int
	if d.dirPos < 0 || d.dirPos%4 != 0 {
		return
	}
	for {
		if d.dirPos >= int(d.dataSize) {
			// end of the directory
			return
		}
		if int(d.blockSize)-(d.dirPos%int(d.blockSize)) < 9 {
			fType = fspEntryTypeSkip
		} else {
			fType = d.data[d.dirPos+8]
		}
		if fType == fspEntryTypeEnd {
			d.dirPos = int(d.dataSize)
			continue
		}
		if fType == fspEntryTypeSkip {
			d.dirPos = (d.dirPos/int(d.blockSize) + 1) * int(d.blockSize)
			continue
		}
		if entry == nil {
			entry = &dirEntry{}
		}
		entry.LastModify = int64(binary.BigEndian.Uint32(d.data[d.dirPos:]))
		entry.Size = uint(binary.BigEndian.Uint32(d.data[d.dirPos+4:]))
		entry.Type = fType
		d.dirPos += 9
		for l := d.dirPos; l < int(d.dataSize); l++ {
			if d.data[d.dirPos+nameLen] == 0 {
				break
			}
			nameLen++
		}
		if nameLen <= 0 {
			entry = nil
			return
		}
		entry.Name = string(d.data[d.dirPos : d.dirPos+nameLen])
		entry.NameLen = uint16(nameLen)
		d.dirPos += nameLen + 1
		entry.RecordLen = uint16(nameLen) + 10
		if entry.RecordLen%4 != 0 {
			n := 4 - entry.RecordLen%4
			entry.RecordLen += n
			d.dirPos += int(n)
		}
		break
	}
	return
}

// File fsp file handle
type File struct {
	s       *Session
	name    string
	writing bool
	eof     bool
	err     uint8
	buffPos int
	pos     uint32
	out     fspPacket
}

// Read bytes from the file
func (f *File) Read(buff []byte, size, count int) (done int, err error) {
	var total = size * count
	var resp fspPacket
	if f.eof {
		return
	}
	for {
		f.out.pos = f.pos
		resp, err = f.s.transaction(&f.out)
		if err != nil {
			return
		}
		if resp.len == 0 {
			f.eof = true
			return
		}
		f.pos += uint32(resp.len)
		copy(buff[done:], resp.buf[:resp.len])
		done += int(resp.len)
		if done >= total {
			break
		}
	}
	return
}

// Write bytes to file
func (f *File) Write(buff []byte) (err error) {
	var total, done int
	var freeBytes, pos int
	if f.eof || f.err != 0 {
		return
	}
	if len(f.out.buf) == 0 {
		f.out.buf = make([]byte, FSPSpace)
	}
	f.out.len = FSPSpace
	total = len(buff)
	done = 0
	pos = 0
	for {
		if f.buffPos >= FSPSpace {
			f.out.pos = f.pos
			_, err = f.s.transaction(&f.out)
			if err != nil {
				f.err = 1
				break
			}
			f.buffPos = 0
			f.pos += uint32(f.out.len)
			done += int(f.out.len)
		}
		freeBytes = FSPSpace - f.buffPos
		if freeBytes <= total {
			copy(f.out.buf[f.buffPos:], buff[pos:pos+freeBytes])
			pos += freeBytes
			f.buffPos = FSPSpace
			total -= freeBytes
		} else {
			copy(f.out.buf[f.buffPos:], buff[pos:pos+total])
			f.buffPos += total
			break
		}
	}
	return
}

// Flush send file buff to server
func (f *File) Flush() (err error) {
	if f.writing == false {
		err = newOpError("bad file")
		return
	}
	if f.eof || f.buffPos == 0 {
		return
	}
	f.out.pos = f.pos
	f.out.len = uint16(f.buffPos)
	_, err = f.s.transaction(&f.out)
	if err != nil {
		f.err = 1
		return
	}
	f.buffPos = 0
	f.pos += uint32(f.out.len)
	return
}

// install finish upload file
func (f *File) install(timeStamp int64) (err error) {
	var out fspPacket
	out.cmd = FSPCommandInstall
	out.xlen = 0
	out.pos = 0
	err = out.buildFileName(f.name, f.s.password)
	if err != nil {
		return
	}
	if timeStamp != 0 {
		out.buf = append(out.buf, []byte{0, 0, 0, 0}...)
		binary.BigEndian.PutUint32(out.buf[out.len:], uint32(timeStamp))
		out.xlen = 4
		out.pos = 4
	}
	_, err = f.s.transaction(&f.out)
	return
}

// Close close fsp file
func (f *File) Close() (err error) {
	if f.writing {
		err = f.Flush()
		if err == nil {
			err = f.install(time.Now().Unix())
		}
	}
	return
}
