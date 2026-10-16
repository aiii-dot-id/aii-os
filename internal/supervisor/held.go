package supervisor

func (s *Supervisor) Held() (bytes int64, known bool) {
	pid := s.Pid()
	if pid == 0 {
		return 0, false
	}
	return treeHeld(pid)
}

func descendants(root int, parent map[int]int) []int {
	if _, listed := parent[root]; !listed {
		return nil
	}
	children := map[int][]int{}
	for pid, ppid := range parent {
		if pid != root {
			children[ppid] = append(children[ppid], pid)
		}
	}
	tree, seen := []int{root}, map[int]bool{root: true}
	for i := 0; i < len(tree); i++ {
		for _, c := range children[tree[i]] {
			if !seen[c] {
				seen[c] = true
				tree = append(tree, c)
			}
		}
	}
	return tree
}
