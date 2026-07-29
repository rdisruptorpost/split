//go:build windows

package agent

import (
	"errors"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

func snapshotProcesses() ([]processInfo, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)

	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, err
	}

	processes := make([]processInfo, 0, 256)
	for {
		processes = append(processes, processInfo{
			pid:       entry.ProcessID,
			parentPID: entry.ParentProcessID,
			name:      windows.UTF16ToString(entry.ExeFile[:]),
		})
		err := windows.Process32Next(snapshot, &entry)
		if errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
			return processes, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func processStartTime(pid uint32) (time.Time, bool) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return time.Time{}, false
	}
	defer windows.CloseHandle(process)

	var created windows.Filetime
	var exited windows.Filetime
	var kernel windows.Filetime
	var user windows.Filetime
	if err := windows.GetProcessTimes(
		process,
		&created,
		&exited,
		&kernel,
		&user,
	); err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, created.Nanoseconds()), true
}

func processCommandLine(pid uint32) string {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(process)

	var size uint32
	_ = windows.NtQueryInformationProcess(
		process,
		windows.ProcessCommandLineInformation,
		nil,
		0,
		&size,
	)
	if size == 0 {
		return ""
	}

	buffer := make([]byte, size+2)
	if err := windows.NtQueryInformationProcess(
		process,
		windows.ProcessCommandLineInformation,
		unsafe.Pointer(&buffer[0]),
		uint32(len(buffer)),
		&size,
	); err != nil {
		return ""
	}

	commandLine := (*windows.NTUnicodeString)(unsafe.Pointer(&buffer[0]))
	if commandLine.Buffer == nil || commandLine.Length == 0 {
		return ""
	}
	characters := unsafe.Slice(commandLine.Buffer, int(commandLine.Length/2))
	return string(utf16.Decode(characters))
}
