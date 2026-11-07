package cohort

import "syscall"

// ioctlGetTermios is the ioctl that reads a terminal's attributes.
const ioctlGetTermios = syscall.TIOCGETA
