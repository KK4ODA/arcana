package apps

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// configureProcess keeps the program from opening a console window.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

// terminate ends the program. Windows has no signal a console-less
// program can catch, so it is killed; the programs keep their data in
// SQLite databases, which survive it.
func terminate(cmd *exec.Cmd) { _ = cmd.Process.Kill() }

// jobHandle is a job object that kills the program, and anything it
// started, when Arcana exits for any reason: Windows closes the handle
// with the process that holds it.
type jobHandle struct{ h windows.Handle }

func attachJob(cmd *exec.Cmd) jobHandle {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return jobHandle{}
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return jobHandle{}
	}
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return jobHandle{}
	}
	defer windows.CloseHandle(ph)
	if err := windows.AssignProcessToJobObject(job, ph); err != nil {
		windows.CloseHandle(job)
		return jobHandle{}
	}
	return jobHandle{h: job}
}

func (j jobHandle) close() {
	if j.h != 0 {
		windows.CloseHandle(j.h)
	}
}
