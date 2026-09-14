package carrd

import (
	"math"
	"testing"
)

// .
// .
// .
// .
func TestTheFiveClassesCarryTheCEditionConstants(t *testing.T) {
	want := map[Class]Params{
		Constitutional: {36500, 0.50},
		Core:           {365, 0.40},
		Standard:       {82, 0.15},
		Operational:    {21, 0.05},
		Ephemeral:      {5, 0.02},
	}
	if got := Classes(); len(got) != 5 {
		t.Fatalf("Classes() = %v, want five", got)
	}
	for c, p := range want {
		got, ok := c.Params()
		if !ok || got != p {
			t.Errorf("%s: params %+v ok=%v, want %+v", c, got, ok, p)
		}
	}
	for i, c := range Classes() {
		if i > 0 {
			prev, _ := Classes()[i-1].Params()
			cur, _ := c.Params()
			if !(prev.HalfLifeDays > cur.HalfLifeDays && prev.Floor > cur.Floor) {
				t.Errorf("classes must be listed most durable first: %s then %s", Classes()[i-1], c)
			}
		}
	}
}

func TestParseForgivesCaseAndNothingElse(t *testing.T) {
	if c, ok := Parse(" Core "); !ok || c != Core {
		t.Fatalf("Parse(\" Core \") = %q, %v", c, ok)
	}
	for _, bad := range []string{"", "cores", "constitution", "ring3"} {
		if _, ok := Parse(bad); ok {
			t.Errorf("Parse(%q) accepted an unknown class", bad)
		}
	}
	if _, ok := Strength(Class("nothing"), 1, 0); ok {
		t.Error("Strength accepted an unknown class")
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestStrengthIsTheCEditionFormula(t *testing.T) {
	// .
	for _, c := range Classes() {
		if s, _ := Strength(c, 0, 0); !near(s, 1) {
			t.Errorf("%s at age 0: %v, want 1", c, s)
		}
	}
	// .
	for _, c := range Classes() {
		p, _ := c.Params()
		if s, _ := Strength(c, p.HalfLifeDays, 0); !near(s, 0.5) {
			t.Errorf("%s at one half-life: %v, want 0.5", c, s)
		}
	}
	// .
	for _, c := range Classes() {
		p, _ := c.Params()
		if s, _ := Strength(c, 1e7, 0); !near(s, p.Floor) {
			t.Errorf("%s at great age: %v, want its floor %v", c, s, p.Floor)
		}
	}
	// .
	// .
	if got := EffectiveAge(100, 0); !near(got, 100) {
		t.Errorf("EffectiveAge(100, 0) = %v", got)
	}
	if got := EffectiveAge(100, 9); !near(got, 100/(1+0.5*math.Log(10))) {
		t.Errorf("EffectiveAge(100, 9) = %v", got)
	}
	once, _ := Strength(Standard, 82, 0)
	often, _ := Strength(Standard, 82, 10)
	if !(often > once && often > 0.5) {
		t.Errorf("reinforcement must slow decay: never=%v, ten recalls=%v", once, often)
	}
}

func TestStrengthIsMonotone(t *testing.T) {
	prev := 2.0
	for age := 0.0; age <= 400; age += 1 {
		s, _ := Strength(Operational, age, 2)
		if s > prev+1e-12 {
			t.Fatalf("strength rose with age at %v days: %v > %v", age, s, prev)
		}
		prev = s
	}
	prev = 0
	for n := int64(0); n < 50; n++ {
		s, _ := Strength(Core, 200, n)
		if s < prev-1e-12 {
			t.Fatalf("strength fell with more recalls at %d: %v < %v", n, s, prev)
		}
		prev = s
	}
}

func TestNegativeInputsReadAsZero(t *testing.T) {
	if s, _ := Strength(Core, -5, -3); !near(s, 1) {
		t.Errorf("negative age and count must read as fresh and never recalled: %v", s)
	}
	if s, _ := Strength(Core, math.NaN(), 0); !near(s, 1) {
		t.Errorf("a NaN age must read as zero: %v", s)
	}
}

func TestFinalIsTheProduct(t *testing.T) {
	if got := Final(0.5, 2, 0.25, 1); !near(got, 0.25) {
		t.Errorf("Final = %v", got)
	}
	if got := Final(0.1, 1, 1, 1); !near(got, 0.1) {
		t.Errorf("neutral factors must not change the score: %v", got)
	}
}
