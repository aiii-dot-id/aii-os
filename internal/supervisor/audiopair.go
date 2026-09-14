package supervisor

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

import "os"

// .
// .
// .
type audioPair struct {
	hostIn     *os.File
	hostOut    *os.File
	childClose func()
}

// .
// .
func (p *audioPair) release(hostToo bool) {
	if p == nil {
		return
	}
	if p.childClose != nil {
		p.childClose()
		p.childClose = nil
	}
	if hostToo {
		for _, f := range []*os.File{p.hostIn, p.hostOut} {
			if f != nil {
				_ = f.Close()
			}
		}
	}
}

// .
func (p *audioPair) ends() (*os.File, *os.File) {
	if p == nil {
		return nil, nil
	}
	return p.hostIn, p.hostOut
}
