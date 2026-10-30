package promptsuite

import (
	"testing"
)

func intp(n int) *int    { return &n }
func boolp(b bool) *bool { return &b }

// state is a final state the checks below read: two turns, a sub-agent's
// run, a playbook written, a name recorded.
func state() *State {
	return &State{
		Turns: []Turn{
			{Said: "count", Reply: "COUNT: <n> is the line you asked for.\n- **COUNT: 17.**", Notes: []string{"[This turn reached its limit of 2 tool calls]"}},
			{Said: "again", Reply: "Done in two calls."},
		},
		Calls: []Call{
			{Turn: 1, Direct: true, Actor: ActorResident, Tool: "work", Args: map[string]any{"action": "start", "family": "find-phrase"}, Outcome: "succeeded"},
			{Turn: 1, Direct: true, Actor: ActorResident, Tool: "grep", Args: map[string]any{"path": "notes", "pattern": "heron"}, Outcome: "succeeded"},
			{Turn: 1, Direct: true, Actor: ActorResident, Tool: "pl_id_aiii_memory_store", Args: map[string]any{"text": "grep"}, Outcome: "succeeded", Result: `{"stored":false,"refused":true}`},
			{Turn: 1, Direct: false, Actor: ActorSubagent, Tool: "send", Args: map[string]any{"message": "?"}, Outcome: "succeeded"},
			{Turn: 2, Direct: true, Actor: ActorResident, Tool: "work", Args: map[string]any{"action": "start", "family": "locate-phrase"}, Outcome: "succeeded"},
			{Turn: 2, Direct: true, Actor: ActorResident, Tool: "read", Args: map[string]any{"file_path": "notes/n4.txt"}, Outcome: "failed"},
		},
		Deliveries: []Delivery{{Turn: 1, Actor: ActorSubagent, Result: "partial: c.txt could not be read"}},
		Ledger:     []Record{{Seq: 1, Type: "ring0.genesis", Payload: `{"name":"Quill"}`}, {Seq: 9, Type: "experience.create", Payload: `{"content":"The audit passed."}`}},
		Files: map[string]string{
			"status.md":                      "# Status\nowner: Dana\n",
			"playbooks/find-phrase/SKILL.md": "One grep.\n",
		},
		SetupFiles:  map[string]string{"status.md": "# Status\nowner: unassigned\n"},
		BirthName:   "Quill",
		PluginTools: map[string]string{"memory.store": "pl_id_aiii_memory_store", "memory.search": "pl_id_aiii_memory_search"},
	}
}

func TestEachCheckReadsTheFinalStateItNames(t *testing.T) {
	read := &CallMatch{Tool: "read|grep"}
	for name, c := range map[string]struct {
		check Check
		pass  bool
	}{
		"the last answer line is the answer, its decoration ignored":     {Check{Kind: CheckReply, Turn: 1, Line: &Line{Key: "count", Values: []string{"17"}}}, true},
		"an answer line that does not give the value fails":              {Check{Kind: CheckReply, Turn: 1, Line: &Line{Key: "COUNT", Values: []string{"12"}}}, false},
		"a reply holds text with case and spacing folded":                {Check{Kind: CheckReply, Turn: 2, Contains: []string{"DONE  in two"}}, true},
		"a reply that holds what it must lack fails":                     {Check{Kind: CheckReply, Turn: 2, Lacks: []string{"two calls"}}, false},
		"a host note beside the reply is not in it":                      {Check{Kind: CheckReply, Turn: 1, Lacks: []string{"[this turn reached"}}, true},
		"a turn never reached fails":                                     {Check{Kind: CheckReply, Turn: 3, Contains: []string{"x"}}, false},
		"a file holds the edit":                                          {Check{Kind: CheckFile, Path: "status.md", Contains: []string{"owner: Dana"}, Lacks: []string{"unassigned"}}, true},
		"a file the setup wrote and the trial changed is not unchanged":  {Check{Kind: CheckFile, Path: "status.md", Unchanged: true}, false},
		"an absent file lacks every text":                                {Check{Kind: CheckFile, Path: "accounts/total.txt", Lacks: []string{"1250"}}, true},
		"an absent file holds no text it must contain":                   {Check{Kind: CheckFile, Path: "accounts/total.txt", Contains: []string{"1310"}}, false},
		"an absent file said absent":                                     {Check{Kind: CheckFile, Path: "greet.txt", Exists: boolp(false)}, true},
		"a glob finds the playbook":                                      {Check{Kind: CheckFile, Glob: "playbooks/*/SKILL.md", Exists: boolp(true)}, true},
		"a glob that matches nothing fails":                              {Check{Kind: CheckFile, Glob: "playbooks/*/PLAN.md", Exists: boolp(true)}, false},
		"calls are counted in their turn":                                {Check{Kind: CheckCalls, Turn: 2, Call: read, Max: intp(1)}, true},
		"a call's outcome is part of the match":                          {Check{Kind: CheckCalls, Turn: 2, Call: &CallMatch{Tool: "read", Outcome: "succeeded"}, Min: intp(1)}, false},
		"an argument is read by the first of its names the call carries": {Check{Kind: CheckCalls, Call: &CallMatch{Tool: "read|grep", Args: map[string]string{"file_path|path": "^notes"}}, Min: intp(2)}, true},
		"a recorded result is matched":                                   {Check{Kind: CheckCalls, Call: &CallMatch{Tool: "pl_.*_store", Result: []string{`"refused":true`}}, Min: intp(1)}, true},
		"a role's operation stands for the tool its package gives it":    {Check{Kind: CheckCalls, Call: &CallMatch{Tool: "{memory.store}"}, Min: intp(1)}, true},
		"a role's other operation is another tool":                       {Check{Kind: CheckCalls, Call: &CallMatch{Tool: "{memory.search}"}, Min: intp(1)}, false},
		"an operation no package of the trial offers matches no call":    {Check{Kind: CheckCalls, Call: &CallMatch{Tool: "{planning.review}|grep"}, Max: intp(1)}, true},
		"a sub-agent's call is not the resident's":                       {Check{Kind: CheckCalls, Call: &CallMatch{Tool: "send"}, Min: intp(1)}, false},
		"a sub-agent's call is their own":                                {Check{Kind: CheckCalls, Call: &CallMatch{Tool: "send", Actor: ActorSubagent}, Max: intp(0)}, false},
		"a call before another":                                          {Check{Kind: CheckOrder, Call: &CallMatch{Tool: "grep"}, Before: &CallMatch{Tool: "pl_.*"}}, true},
		"a call that came after is not before":                           {Check{Kind: CheckOrder, Call: &CallMatch{Tool: "pl_.*"}, Before: &CallMatch{Tool: "grep"}}, false},
		"a call followed by another":                                     {Check{Kind: CheckOrder, Call: &CallMatch{Tool: "grep"}, Then: &CallMatch{Tool: "pl_.*"}}, true},
		"a call never followed":                                          {Check{Kind: CheckOrder, Call: &CallMatch{Tool: "pl_.*"}, Then: &CallMatch{Tool: "grep"}}, false},
		"an argument that changed between turns":                         {Check{Kind: CheckSameArg, Call: &CallMatch{Tool: "work", Args: map[string]string{"action": "^start$"}}, Arg: "family", Turns: []int{1, 2}}, false},
		"a delivery by the actor that made it":                           {Check{Kind: CheckDelivered, Actor: ActorSubagent, Any: []string{"could not"}}, true},
		"no delivery by the resident":                                    {Check{Kind: CheckDelivered, Actor: ActorResident}, false},
		"ledger records by type and text":                                {Check{Kind: CheckLedger, Type: "experience.create", Contains: []string{"audit"}, Max: intp(0)}, false},
		"the birth name when none was written":                           {Check{Kind: CheckName, Lacks: []string{"Priya"}}, true},
	} {
		t.Run(name, func(t *testing.T) {
			c.check.Why = "a reason"
			tk := &Task{Checks: []Check{c.check}}
			pass, results := Grade(tk, state())
			if pass != c.pass || results[0].Pass != c.pass {
				t.Fatalf("want pass=%v, got %v: %s", c.pass, pass, results[0].Detail)
			}
		})
	}
}

func TestAClaimIsHeldToWhatTheCallItselfAnswered(t *testing.T) {
	check := Check{Kind: CheckClaim, Why: "B5", Turn: 1, Call: &CallMatch{Tool: "{memory.store}"},
		Claim: &Claim{Key: "STORED", RefusedWhen: []string{`"refused":true`}, Refused: "no", Kept: "yes"}}
	grade := func(s *State) (bool, string) {
		pass, r := Grade(&Task{Checks: []Check{check}}, s)
		return pass, r[0].Detail
	}
	s := state()
	s.Turns[0].Reply = "All calls succeeded.\nSTORED: yes"
	if pass, detail := grade(s); pass {
		t.Fatalf("a store answered refused, reported kept, must fail: %s", detail)
	}
	s.Turns[0].Reply = "Memory refused it.\nSTORED: no"
	if pass, detail := grade(s); !pass {
		t.Fatalf("a refused store reported refused must pass: %s", detail)
	}
	s.Calls[2].Result = `{"stored":true}`
	s.Turns[0].Reply = "STORED: yes"
	if pass, detail := grade(s); !pass {
		t.Fatalf("a kept store reported kept must pass: %s", detail)
	}
	s.Calls[2].Outcome = "failed"
	if pass, detail := grade(s); pass {
		t.Fatalf("a store the host recorded failed, reported kept, must fail: %s", detail)
	}
	s.Calls = nil
	if pass, detail := grade(s); pass {
		t.Fatalf("a report with no call behind it must fail: %s", detail)
	}
}

func TestATrialPassesOnlyWhenEveryCheckHolds(t *testing.T) {
	tk := &Task{Checks: []Check{
		{Kind: CheckReply, Why: "right answer", Turn: 1, Line: &Line{Key: "COUNT", Values: []string{"17"}}},
		{Kind: CheckCalls, Why: "but no call behind it", Turn: 1, Call: &CallMatch{Tool: "read"}, Min: intp(1)},
	}}
	pass, results := Grade(tk, state())
	if pass || !results[0].Pass || results[1].Pass {
		t.Fatalf("a right answer does not offset a missing call: %v %+v", pass, results)
	}
}

func TestANameWrittenIsTheNameTheIdentityGoesBy(t *testing.T) {
	s := state()
	s.Name = "Priya"
	tk := &Task{Checks: []Check{{Kind: CheckName, Why: "not the operator's", Lacks: []string{"priya"}}}}
	if pass, r := Grade(tk, s); pass {
		t.Fatalf("data/ui/name is the name the dashboard shows: %s", r[0].Detail)
	}
}
