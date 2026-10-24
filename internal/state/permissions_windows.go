package state

import (
	"golang.org/x/sys/windows"
	"os/user"
)

func protectDir(path string) error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	// A protected DACL replaces inherited and pre-existing broad grants. Children
	// inherit this user's full-control ACE without spawning a utility every tick.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + u.Uid + ")")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
