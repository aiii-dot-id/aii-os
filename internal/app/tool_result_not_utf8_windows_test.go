package app

// rawBytes is a PowerShell command that writes exactly these bytes to its
// output, whatever the console's code page: the first three rows do not
// depend on which PowerShell the shell tool finds.
func rawBytes(list string) string {
	return `$o = [Console]::OpenStandardOutput(); $b = [byte[]](` + list + `); $o.Write($b, 0, $b.Length); $o.Flush()`
}

var notUTF8Commands = []notUTF8Command{
	{name: "a short line in another code page", command: rawBytes("99,97,102,233,32,97,117,32,108,97,105,116,10")},
	{name: "more than the record keeps", command: rawBytes("99,97,102,233,10") + `; 1..2000 | ForEach-Object { $_ }`},
	{name: "the head of a file that is not text", command: rawBytes("80,75,3,4,20,0,6,0,255,254")},
	// What the laptop ran: text, written by the shell in its own encoding.
	{name: "a letter outside ASCII written by the shell itself", command: `Write-Output ('caf' + [char]233 + ' au lait')`, encodingIsTheShells: true},
}
