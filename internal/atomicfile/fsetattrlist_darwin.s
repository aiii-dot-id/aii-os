//go:build darwin || ios

#include "textflag.h"

TEXT libc_fsetattrlist_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_fsetattrlist(SB)
GLOBL	·libc_fsetattrlist_trampoline_addr(SB), RODATA, $8
DATA	·libc_fsetattrlist_trampoline_addr(SB)/8, $libc_fsetattrlist_trampoline<>(SB)
