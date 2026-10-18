//go:build !windows

package app

var notUTF8Commands = []notUTF8Command{
	{name: "a short line in another code page", command: `printf 'caf\351 au lait\n'`},
	{name: "more than the record keeps", command: `printf 'caf\351\n'; seq 1 2000`},
	{name: "the head of a file that is not text", command: `printf 'PK\003\004\024\000\006\000\377\376'`},
}
