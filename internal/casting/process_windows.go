package casting

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var createJobObject = kernel32.NewProc("CreateJobObjectW")
var setInformationJobObject = kernel32.NewProc("SetInformationJobObject")
var terminateJobObject = kernel32.NewProc("TerminateJobObject")
var initializeProcThreadAttributeList = kernel32.NewProc("InitializeProcThreadAttributeList")
var updateProcThreadAttribute = kernel32.NewProc("UpdateProcThreadAttribute")
var deleteProcThreadAttributeList = kernel32.NewProc("DeleteProcThreadAttributeList")
var resumeThread = kernel32.NewProc("ResumeThread")

type windowsChild struct {
	process syscall.Handle
	job     syscall.Handle
}

type startupInfoEx struct {
	syscall.StartupInfo
	attributes unsafe.Pointer
}

func lockProcessThread() func() { return func() {} }

func startChild(path string, stdin, stdout, stderr *os.File) (childProcess, error) {
	job, _, err := createJobObject.Call(0, 0)
	if job == 0 {
		return nil, err
	}
	owned := false
	defer func() {
		if !owned {
			syscall.CloseHandle(syscall.Handle(job))
		}
	}()
	// LimitFlags has the same offset in the Win32 and Win64 native layouts.
	limits := [18]uint64{}
	*(*uint32)(unsafe.Add(unsafe.Pointer(&limits[0]), 16)) = 0x00002000
	limitSize := unsafe.Sizeof(limits)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		limitSize = 112
	}
	ok, _, err := setInformationJobObject.Call(job, 9, uintptr(unsafe.Pointer(&limits[0])), limitSize)
	if ok == 0 {
		return nil, err
	}
	var size uintptr
	initializeProcThreadAttributeList.Call(0, 2, 0, uintptr(unsafe.Pointer(&size)))
	if size == 0 || size > 1024*1024 {
		return nil, errors.New("cannot allocate process attributes")
	}
	ptrSize := unsafe.Sizeof(uintptr(0))
	attributes := make([]uintptr, (size+4*ptrSize+ptrSize-1)/ptrSize)
	allocation := unsafe.Pointer(&attributes[0])
	var pinned runtime.Pinner
	pinned.Pin(&attributes[0])
	defer pinned.Unpin()
	ok, _, err = initializeProcThreadAttributeList.Call(uintptr(allocation), 2, 0, uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		return nil, err
	}
	defer deleteProcThreadAttributeList.Call(uintptr(allocation))
	// The OS retains attribute pointers across calls, so pin the backing memory.
	handles := unsafe.Slice((*syscall.Handle)(unsafe.Add(allocation, size)), 4)
	self, err := syscall.GetCurrentProcess()
	if err != nil {
		return nil, err
	}
	for i, file := range []*os.File{stdin, stdout, stderr} {
		if err := syscall.DuplicateHandle(self, syscall.Handle(file.Fd()), self, &handles[i], 0, true, syscall.DUPLICATE_SAME_ACCESS); err != nil {
			return nil, err
		}
		defer syscall.CloseHandle(handles[i])
	}
	ok, _, err = updateProcThreadAttribute.Call(uintptr(allocation), 0, 0x00020002, uintptr(unsafe.Pointer(&handles[0])), 3*ptrSize, 0, 0)
	if ok == 0 {
		return nil, err
	}
	handles[3] = syscall.Handle(job)
	// JOB_LIST assigns the job atomically at creation. A parent crash cannot leave
	// an unassigned suspended child. Unsupported Windows versions fail closed.
	ok, _, err = updateProcThreadAttribute.Call(uintptr(allocation), 0, 0x0002000d, uintptr(unsafe.Pointer(&handles[3])), ptrSize, 0, 0)
	if ok == 0 {
		return nil, err
	}
	application, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	arguments, err := syscall.UTF16FromString(syscall.EscapeArg(path))
	if err != nil {
		return nil, err
	}
	directory, err := syscall.UTF16PtrFromString(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	info := startupInfoEx{attributes: allocation}
	info.Cb = uint32(unsafe.Sizeof(info))
	info.Flags = syscall.STARTF_USESTDHANDLES | syscall.STARTF_USESHOWWINDOW
	info.ShowWindow = syscall.SW_HIDE
	info.StdInput, info.StdOutput, info.StdErr = handles[0], handles[1], handles[2]
	var process syscall.ProcessInformation
	err = syscall.CreateProcess(application, &arguments[0], nil, nil, true, 0x00000004|0x00080000|0x00000400|0x08000000, nil, directory, &info.StartupInfo, &process)
	runtime.KeepAlive(handles)
	if err != nil {
		return nil, err
	}
	defer syscall.CloseHandle(process.Thread)
	child := &windowsChild{process: process.Process, job: syscall.Handle(job)}
	owned = true
	resumed, _, err := resumeThread.Call(uintptr(process.Thread))
	if uint32(resumed) == 0xffffffff {
		child.kill()
		syscall.WaitForSingleObject(child.process, 5000)
		child.close()
		return nil, err
	}
	return child, nil
}

func (p *windowsChild) wait() error {
	result, err := syscall.WaitForSingleObject(p.process, syscall.INFINITE)
	if err != nil || result != syscall.WAIT_OBJECT_0 {
		return errors.New("cannot wait for the casting engine")
	}
	var code uint32
	if err := syscall.GetExitCodeProcess(p.process, &code); err != nil {
		return err
	}
	if code != 0 {
		return errors.New("the casting engine exited unsuccessfully")
	}
	return nil
}

func (p *windowsChild) kill() error {
	ok, _, err := terminateJobObject.Call(uintptr(p.job), 1)
	if ok == 0 {
		syscall.TerminateProcess(p.process, 1)
		return err
	}
	return nil
}

func (p *windowsChild) close() error {
	err := syscall.CloseHandle(p.job)
	syscall.CloseHandle(p.process)
	return err
}
