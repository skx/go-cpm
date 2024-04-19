// This file contains the implementations for the CP/M calls we emulate.
//
// NOTE: They are added to the syscalls map in cpm.go
//

package cpm

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/skx/cpmulator/fcb"
	"golang.org/x/term"
)

// blkSize is the size of block-based I/O operations
const blkSize = 128

// maxRC is the maximum read count
const maxRC = 128

// SysCallExit implements the Exit syscall
func SysCallExit(cpm *CPM) error {
	return ErrExit
}

// SysCallReadChar reads a single character from the console.
func SysCallReadChar(cpm *CPM) error {
	// switch stdin into 'raw' mode
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("error making raw terminal %s", err)
	}

	// read only a single byte
	b := make([]byte, 1)
	_, err = os.Stdin.Read(b)
	if err != nil {
		return fmt.Errorf("error reading a byte from stdin %s", err)
	}

	// restore the state of the terminal to avoid mixing RAW/Cooked
	err = term.Restore(int(os.Stdin.Fd()), oldState)
	if err != nil {
		return fmt.Errorf("error restoring terminal state %s", err)
	}

	// Return the character
	cpm.CPU.States.AF.Hi = b[0]

	return nil
}

// SysCallWriteChar writes the single character in the A register to STDOUT.
func SysCallWriteChar(cpm *CPM) error {
	fmt.Printf("%c", (cpm.CPU.States.DE.Lo))
	return nil
}

func SysCallRawIO(cpm *CPM) error {
	if cpm.CPU.States.DE.Lo != 0xff {
		fmt.Printf("%c", cpm.CPU.States.DE.Lo)
	} else {
		return SysCallReadChar(cpm)
	}

	return nil
}

// SysCallWriteString writes the $-terminated string pointed to by DE to STDOUT
func SysCallWriteString(cpm *CPM) error {
	addr := cpm.CPU.States.DE.U16()

	c := cpm.Memory.Get(addr)
	for c != '$' {
		fmt.Printf("%c", c)
		addr++
		c = cpm.Memory.Get(addr)
	}
	return nil
}

// SysCallReadString reads a string from the console, into the buffer pointed to by DE.
func SysCallReadString(cpm *CPM) error {
	addr := cpm.CPU.States.DE.U16()

	text, err := cpm.Reader.ReadString('\n')
	if err != nil {
		return (fmt.Errorf("error reading from STDIN:%s", err))
	}

	// remove trailing newline
	text = strings.TrimSuffix(text, "\n")

	// addr[0] is the size of the input buffer
	// addr[1] should be the size of input read, set it:
	cpm.CPU.Memory.Set(addr+1, uint8(len(text)))

	// addr[2+] should be the text
	i := 0
	for i < len(text) {
		cpm.CPU.Memory.Set(uint16(addr+2+uint16(i)), text[i])
		i++
	}

	return nil
}

// SysCallSetDMA updates the address of the DMA area, which is used for block I/O.
func SysCallSetDMA(cpm *CPM) error {

	// Get the address from BC
	addr := cpm.CPU.States.DE.U16()

	// Update the DMA value.
	cpm.dma = addr

	return nil
}

// SysCallDriveAllReset resets the drives
func SysCallDriveAllReset(cpm *CPM) error {
	cpm.currentDrive = 1
	cpm.userNumber = 0
	cpm.CPU.States.AF.Hi = 0x00
	return nil
}

// SysCallDriveSet updates the current drive number
func SysCallDriveSet(cpm *CPM) error {
	// The drive number passed to this routine is 0 for A:, 1 for B: up to 15 for P:.
	cpm.currentDrive = (cpm.CPU.States.AF.Hi & 0x0F)

	// Success means we return 0x00 in A
	cpm.CPU.States.AF.Hi = 0x00

	return nil
}

// SysCallFileOpen opens the filename that matches the pattern on the FCB supplied in DE
func SysCallFileOpen(cpm *CPM) error {

	// The pointer to the FCB
	ptr := cpm.CPU.States.DE.U16()

	// Get the bytes which make up the FCB entry.
	xxx := cpm.Memory.GetRange(ptr, 36)

	// Create a structure with the contents
	fcbPtr := fcb.FromBytes(xxx)

	// Get the parts of the name
	name := fcbPtr.GetName()
	ext := fcbPtr.GetType()

	// Get the actual name
	fileName := name
	if ext != "" && ext != "   " {
		fileName += "."
		fileName += ext
	}

	// Now we open..
	file, err := os.OpenFile(fileName, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}

	// Save the file handle in our cache.
	cpm.files[ptr] = FileCache{name: fileName, handle: file}

	// Get file size, in bytes
	fi, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file size of %s: %s", fileName, err)
	}

	// Get file size, in bytes
	fileSize := fi.Size()

	// Get file size, in blocks
	fLen := uint8(fileSize / blkSize)

	// Set record-count
	if fLen > maxRC {
		fcbPtr.RC = maxRC
	} else {
		fcbPtr.RC = fLen
	}

	// Update the FCB in memory.
	cpm.Memory.PutRange(ptr, fcbPtr.AsBytes()...)

	// Return success
	cpm.CPU.States.AF.Hi = 0x00

	return nil
}

// SysCallFileClose closes the filename that matches the pattern on the FCB supplied in DE
func SysCallFileClose(cpm *CPM) error {

	// The pointer to the FCB
	ptr := cpm.CPU.States.DE.U16()

	// Get the file handle from our cache.
	obj, ok := cpm.files[ptr]
	if !ok {
		return fmt.Errorf("SysCallFileClose: Tried to close a file that wasn't open")
	}

	err := obj.handle.Close()
	if err != nil {
		return fmt.Errorf("failed to close file %04X:%s", ptr, err)
	}
	delete(cpm.files, ptr)

	// Record success
	cpm.CPU.States.AF.Hi = 0x00
	return nil
}

// SysCallFindFirst finds the first filename, on disk, that matches the glob in the FCB supplied in DE.
func SysCallFindFirst(cpm *CPM) error {
	// The pointer to the FCB
	ptr := cpm.CPU.States.DE.U16()
	// Get the bytes which make up the FCB entry.
	xxx := cpm.Memory.GetRange(ptr, 36)

	// Previous results are now invalidated
	cpm.findFirstResults = []string{}
	cpm.findOffset = 0

	// Create a structure with the contents
	fcbPtr := fcb.FromBytes(xxx)

	pattern := ""
	name := fcbPtr.GetName()
	ext := fcbPtr.GetType()

	for _, c := range name {
		if c == '?' {
			pattern += "*"
			break
		}
		if c == ' ' {
			continue
		}
		pattern += string(c)
	}
	if ext != "" && ext != "   " {
		pattern += "."
	}

	for _, c := range ext {
		if c == '?' {
			pattern += "*"
			break
		}
		if c == ' ' {
			continue
		}
		pattern += string(c)
	}

	// Run the glob.
	matches, err := filepath.Glob(pattern)
	if err != nil {
		// error in pattern?
		fmt.Printf("glob error %s\n", err)
		cpm.CPU.States.AF.Hi = 0xFF
		return nil
	}

	// No matches on the glob-search
	if len(matches) == 0 {
		// Return 0xFF for failure
		cpm.CPU.States.AF.Hi = 0xFF
		return nil
	}

	// Here we save the results in our cache,
	// dropping the first
	cpm.findFirstResults = matches[1:]
	cpm.findOffset = 0

	// Create a new FCB and store it in the DMA entry
	x := fcb.FromString(matches[0])
	data := x.AsBytes()
	cpm.Memory.PutRange(cpm.dma, data...)

	// Return 0x00 to point to the first entry in the DMA area.
	cpm.CPU.States.AF.Hi = 0x00

	return nil
}

// SysCallFindNext finds the next filename that matches the glob set in the FCB in DE.
func SysCallFindNext(cpm *CPM) error {
	//
	// Assume we've been called with findFirst before
	//
	if (len(cpm.findFirstResults) == 0) || cpm.findOffset >= len(cpm.findFirstResults) {
		// Return 0xFF to signal an error
		cpm.CPU.States.AF.Hi = 0xFF
		return nil
	}

	res := cpm.findFirstResults[cpm.findOffset]
	cpm.findOffset++

	// Create a new FCB and store it in the DMA entry
	x := fcb.FromString(res)
	data := x.AsBytes()
	cpm.Memory.PutRange(0x80, data...)

	// Return 0x00 to point to the first entry in the DMA area.
	cpm.CPU.States.AF.Hi = 0x00

	return nil
}

// SysCallDeleteFile deletes the filename specified by the FCB in DE.
func SysCallDeleteFile(cpm *CPM) error {

	// The pointer to the FCB
	ptr := cpm.CPU.States.DE.U16()
	// Get the bytes which make up the FCB entry.
	xxx := cpm.Memory.GetRange(ptr, 36)

	// Create a structure with the contents
	fcbPtr := fcb.FromBytes(xxx)

	// Get the name
	name := fcbPtr.GetName()
	ext := fcbPtr.GetType()

	fileName := name
	if ext != "" && ext != "   " {
		fileName += "."
		fileName += ext
	}

	// Delete the named file
	err := os.Remove(fileName)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// SysCallRead reads a record from the file named in the FCB given in DE
func SysCallRead(cpm *CPM) error {

	// The pointer to the FCB
	ptr := cpm.CPU.States.DE.U16()

	// Get the bytes which make up the FCB entry.
	xxx := cpm.Memory.GetRange(ptr, 36)

	// Create a structure with the contents
	fcbPtr := fcb.FromBytes(xxx)

	// Get the file handle in our cache.
	obj, ok := cpm.files[ptr]
	if !ok {
		cpm.Logger.Error("SysCallRead: Attempting to read from a file that isn't open")
		cpm.CPU.States.AF.Hi = 0xff
		return nil
	}

	// Get the next read position
	offset := fcbPtr.GetSequentialOffset()

	_, err := obj.handle.Seek(int64(offset), io.SeekStart)
	if err != nil {
		return fmt.Errorf("cannot seek to position %d: %s", offset, err)
	}

	// Temporary area to read into
	data := make([]byte, blkSize)

	// Fill the area with data
	for i := range data {
		data[i] = 0x1a
	}

	// Read from the file, now we're in the right place
	_, err = obj.handle.Read(data)
	if err != nil && err != io.EOF {
		return fmt.Errorf("error reading file %s", err)
	}

	// Copy the data to the DMA area
	cpm.Memory.PutRange(cpm.dma, data[:]...)

	// Update the next read position
	fcbPtr.IncreaseSequentialOffset()

	// Update the FCB in memory
	cpm.Memory.PutRange(ptr, fcbPtr.AsBytes()...)

	// All done
	if err == io.EOF {
		cpm.CPU.States.AF.Hi = 0x01
	} else {
		cpm.CPU.States.AF.Hi = 0x00
	}
	return nil

}

// SysCallWrite writes a record to the file named in the FCB given in DE
func SysCallWrite(cpm *CPM) error {

	// The pointer to the FCB
	ptr := cpm.CPU.States.DE.U16()
	// Get the bytes which make up the FCB entry.
	xxx := cpm.Memory.GetRange(ptr, 36)

	// Create a structure with the contents
	fcbPtr := fcb.FromBytes(xxx)

	// Get the file handle in our cache.
	obj, ok := cpm.files[ptr]
	if !ok {
		cpm.Logger.Error("SysCallWrite: Attempting to write to a file that isn't open")
		cpm.CPU.States.AF.Hi = 0xff
		return nil
	}

	// Get the next write position
	offset := fcbPtr.GetSequentialOffset()

	// Get the data range from the DMA area
	data := cpm.Memory.GetRange(cpm.dma, 128)

	// Move to the correct place
	_, err := obj.handle.Seek(int64(offset), io.SeekStart)
	if err != nil {
		return fmt.Errorf("cannot seek to position %d: %s", offset, err)
	}

	// Write to the open file
	_, err = obj.handle.Write(data)
	if err != nil {
		return fmt.Errorf("error writing to file %s", err)
	}

	// Update the next write position
	fcbPtr.IncreaseSequentialOffset()

	// Update the FCB in memory
	cpm.Memory.PutRange(ptr, fcbPtr.AsBytes()...)

	// All done
	cpm.CPU.States.AF.Hi = 0x00
	return nil

}

// SysCallMakeFile creates the file named in the FCB given in DE
func SysCallMakeFile(cpm *CPM) error {

	// The pointer to the FCB
	ptr := cpm.CPU.States.DE.U16()
	// Get the bytes which make up the FCB entry.
	xxx := cpm.Memory.GetRange(ptr, 36)

	// Create a structure with the contents
	fcbPtr := fcb.FromBytes(xxx)

	// Get the name
	name := fcbPtr.GetName()
	ext := fcbPtr.GetType()

	fileName := name
	if ext != "" && ext != "   " {
		fileName += "."
		fileName += ext
	}

	// Create the file
	file, err := os.OpenFile(fileName, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}

	// Get file size, in bytes
	fi, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file size of %s: %s", fileName, err)
	}

	// Get file size, in bytes
	fileSize := fi.Size()

	// Get file size, in blocks
	fLen := uint8(fileSize / blkSize)

	// Set record-count
	if fLen > maxRC {
		fcbPtr.RC = maxRC
	} else {
		fcbPtr.RC = fLen
	}

	// Save the file-handle
	cpm.files[ptr] = FileCache{name: fileName, handle: file}

	// Update the FCB in memory
	cpm.Memory.PutRange(ptr, fcbPtr.AsBytes()...)

	cpm.CPU.States.AF.Hi = 0x00
	return nil
}

// SysCallLoginVec returns the list of logged in drives.
func SysCallLoginVec(cpm *CPM) error {
	cpm.CPU.States.HL.Hi = 0xFF
	cpm.CPU.States.HL.Lo = 0xFF
	return nil
}

// SysCallDriveGet returns the number of the active drive.
func SysCallDriveGet(cpm *CPM) error {
	cpm.CPU.States.AF.Hi = cpm.currentDrive

	return nil
}

// SysCallGetDriveDPB returns the address of the DPB, which is faked.
func SysCallGetDriveDPB(cpm *CPM) error {
	cpm.CPU.States.HL.Hi = 0xCD
	cpm.CPU.States.HL.Lo = 0xCD
	return nil
}

func SysCallUserNumber(cpm *CPM) error {

	// We're either setting or getting
	//
	// If the value is 0xFF we return it, otherwise we set
	if cpm.CPU.States.DE.Lo != 0xFF {

		// Set the number - masked, because valid values are 0-15
		cpm.userNumber = (cpm.CPU.States.DE.Lo & 0x0F)
	}

	// Return the current number, which might have changed
	cpm.CPU.States.AF.Hi = cpm.userNumber
	return nil
}

// SysCallReadRand reads a random block from the FCB pointed to by DE into the DMA area.
func SysCallReadRand(cpm *CPM) error {
	// Temporary area to read into
	data := make([]byte, blkSize)

	// sysRead reads from the given offset
	//
	// Return:
	//  0 : read something successfully
	//  1 : read nothing - error really
	//
	sysRead := func(f *os.File, offset int64) int {

		// Get file size, in bytes
		fi, err := f.Stat()
		if err != nil {
			fmt.Printf("ReadRand:failed to get file size of: %s", err)
		}
		fileSize := fi.Size()

		// If the offset we're reading from is bigger than the file size then
		// pad it up
		if offset > fileSize {
			return 06
		}

		_, err = f.Seek(offset, io.SeekStart)
		if err != nil {
			fmt.Printf("cannot seek to position %d: %s", offset, err)
			return 0xff
		}

		for i := range data {
			data[i] = 0x1a
		}

		_, err = f.Read(data)
		if err != nil {
			if err != io.EOF {
				fmt.Printf("failed to read offset %d: %s", offset, err)
				return 0xff
			}
		}

		cpm.Memory.PutRange(cpm.dma, data[:]...)
		return 0
	}

	// The pointer to the FCB
	ptr := cpm.CPU.States.DE.U16()

	// Get the bytes which make up the FCB entry.
	xxx := cpm.Memory.GetRange(ptr, 36)

	// Create a structure with the contents
	fcbPtr := fcb.FromBytes(xxx)

	// Get the file handle in our cache.
	obj, ok := cpm.files[ptr]
	if !ok {
		cpm.Logger.Error("SysCallReadRand: Attempting to read from a file that isn't open")
		cpm.CPU.States.AF.Hi = 0xff
		return nil
	}

	// Get the record to read
	record := int(int(fcbPtr.R2)<<16) | int(int(fcbPtr.R1)<<8) | int(fcbPtr.R0)

	// Translate the record to a byte-offset
	fpos := int64(record) * blkSize

	// Read the data
	res := sysRead(obj.handle, fpos)

	// Update the FCB in memory
	cpm.Memory.PutRange(ptr, fcbPtr.AsBytes()...)
	cpm.CPU.States.AF.Hi = uint8(res)
	return nil
}

// SysCallWriteRand writes a random block from DMA area to the FCB pointed to by DE.
func SysCallWriteRand(cpm *CPM) error {

	// The pointer to the FCB
	ptr := cpm.CPU.States.DE.U16()

	// Get the bytes which make up the FCB entry.
	xxx := cpm.Memory.GetRange(ptr, 36)

	// Create a structure with the contents
	fcbPtr := fcb.FromBytes(xxx)

	// Get the file handle in our cache.
	obj, ok := cpm.files[ptr]
	if !ok {
		cpm.Logger.Error("SysCallWriteRand: Attempting to write to a file that isn't open")
		cpm.CPU.States.AF.Hi = 0xff
		return nil
	}

	// Get the data range from the DMA area
	data := cpm.Memory.GetRange(cpm.dma, 128)

	// Get the record to write
	record := int(int(fcbPtr.R2)<<16) | int(int(fcbPtr.R1)<<8) | int(fcbPtr.R0)

	// Get the file position that translates to
	fpos := int64(record) * blkSize

	// Get file size, in bytes
	fi, err := obj.handle.Stat()
	if err != nil {
		return fmt.Errorf("WriteRand:failed to get file size of: %s", err)
	}
	fileSize := fi.Size()

	// If the offset we're writing to is bigger than the file size then
	// pad it up
	padding := fileSize - fpos

	for padding > 0 {
		_, er := obj.handle.Write([]byte{0x00})
		if er != nil {
			return fmt.Errorf("WriteRand: error adding padding: %s", er)
		}
		padding--
	}

	_, err = obj.handle.Seek(fpos, io.SeekStart)
	if err != nil {
		return fmt.Errorf("SysCallWriteRandcannot seek to position %d: %s\n", fpos, err)
	}

	_, err = obj.handle.Write(data)
	if err != nil {
		return fmt.Errorf("SysCalLWriteRand: failed to write to offset %d: %s\n", fpos, err)
	}

	fcbPtr.IncreaseSequentialOffset()

	// Update the FCB in memory
	cpm.Memory.PutRange(ptr, fcbPtr.AsBytes()...)
	cpm.CPU.States.AF.Hi = 0x00
	return nil
}
