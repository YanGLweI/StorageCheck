package collector

import (
	"fmt"

	"golang.org/x/sys/windows"

	"storagecheck/internal/model"
)

const driveFixed = 3

type windowsCollector struct{}

func New() Collector { return &windowsCollector{} }

func (c *windowsCollector) List() ([]model.DriveStat, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil, fmt.Errorf("GetLogicalDrives: %w", err)
	}
	var out []model.DriveStat
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		letter := rune('A' + i)
		root := string(letter) + `:\`
		rootU16, err := windows.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		if windows.GetDriveType(rootU16) != driveFixed {
			continue
		}
		var freeAvail, totalBytes, totalFree uint64
		if err := windows.GetDiskFreeSpaceEx(rootU16, &freeAvail, &totalBytes, &totalFree); err != nil {
			continue
		}
		out = append(out, MakeStat(string(letter)+":", volumeLabel(root), totalBytes, freeAvail))
	}
	return out, nil
}

func volumeLabel(root string) string {
	rootU16, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return ""
	}
	var buf [261]uint16
	if err := windows.GetVolumeInformation(rootU16, &buf[0], uint32(len(buf)), nil, nil, nil, nil, 0); err != nil {
		return ""
	}
	return windows.UTF16PtrToString(&buf[0])
}
