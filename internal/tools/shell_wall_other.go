//go:build !(darwin && !ios)

package tools

const shellWallDescription = ""

func behindWall(_ string, _ shellWall, bin string, argv []string) (string, []string, error) {
	return bin, argv, nil
}
