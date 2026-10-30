//go:build !(darwin && !ios) && !(linux && !android) && !windows

package tools

import "os"

const shellWallDescription = ""

func behindWall(_ string, _ shellWall, bin string, argv []string) (string, []string, error) {
	return bin, argv, nil
}

func shellWallDescriptionNow() string { return shellWallDescription }

func wallKeepsPlaces() bool { return false }

type shellHolder struct{}

func (shellHolder) end() {}

func (t *ShellTool) behindTheWall(w shellWall, _, bin string, argv []string) (string, []string, []*os.File, func(), error) {
	wbin, wargv, err := behindWall(t.sandbox, w, bin, argv)
	return wbin, wargv, nil, func() {}, err
}

func (t *ShellTool) endWall() { t.holder.end() }
