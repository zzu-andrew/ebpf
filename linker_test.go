package ebpf

import (
	"errors"
	"testing"

	"github.com/cilium/ebpf/asm"
	"github.com/cilium/ebpf/btf"
	"github.com/cilium/ebpf/internal"
	"github.com/cilium/ebpf/internal/testutils"

	"github.com/go-quicktest/qt"
)

func TestFindReferences(t *testing.T) {
	progs := map[string]*ProgramSpec{
		"entrypoint": {
			Type: SocketFilter,
			Instructions: asm.Instructions{
				// Make sure the call doesn't happen at instruction 0
				// to exercise the relative offset calculation.
				asm.Mov.Reg(asm.R0, asm.R1),
				asm.Call.Label("my_func"),
				asm.Return(),
			},
			License: "MIT",
		},
		"my_other_func": {
			Instructions: asm.Instructions{
				asm.LoadImm(asm.R0, 1337, asm.DWord).WithSymbol("my_other_func"),
				asm.Return(),
			},
		},
		"my_func": {
			Instructions: asm.Instructions{
				asm.Call.Label("my_other_func").WithSymbol("my_func"),
				asm.Return(),
			},
		},
	}

	flattenPrograms(progs, []string{"entrypoint"})

	prog, err := newProgram(t, progs["entrypoint"], nil)
	testutils.SkipIfNotSupported(t, err)
	qt.Assert(t, qt.IsNil(err))

	ret, _, err := prog.Test(internal.EmptyBPFContext)
	if err != nil {
		t.Fatal(err)
	}

	if ret != 1337 {
		t.Errorf("Expected return code 1337, got %d", ret)
	}
}

func TestForwardFunctionDeclaration(t *testing.T) {
	file := testutils.NativeFile(t, "testdata/fwd_decl-%s.elf")
	coll, err := LoadCollectionSpec(file)
	if err != nil {
		t.Fatal(err)
	}

	spec := coll.Programs["call_fwd"]

	// This program calls an unimplemented forward function declaration.
	_, err = newProgram(t, spec, nil)
	if !errors.Is(err, asm.ErrUnsatisfiedProgramReference) {
		t.Fatal("Expected an error wrapping ErrUnsatisfiedProgramReference, got:", err)
	}

	// Append the implementation of fwd().
	spec.Instructions = append(spec.Instructions,
		asm.Mov.Imm32(asm.R0, 23).WithSymbol("fwd"),
		asm.Return(),
	)

	// The body of the subprog we appended does not come with BTF func_infos,
	// so the verifier will reject it. Load without BTF.
	for i, ins := range spec.Instructions {
		if btf.FuncMetadata(&ins) != nil || ins.Source() != nil {
			sym := ins.Symbol()
			ref := ins.Reference()
			ins.Metadata = asm.Metadata{}
			spec.Instructions[i] = ins.WithSymbol(sym).WithReference(ref)
		}
	}

	prog, err := newProgram(t, spec, nil)
	testutils.SkipIfNotSupported(t, err)
	qt.Assert(t, qt.IsNil(err))

	ret, _, err := prog.Test(internal.EmptyBPFContext)
	if err != nil {
		t.Fatal("Running program:", err)
	}
	if ret != 23 {
		t.Fatalf("Expected 23, got %d", ret)
	}
}

func TestSplitSymbols(t *testing.T) {

	// Splitting an empty insns results in an error.
	_, err := splitSymbols(asm.Instructions{})
	qt.Assert(t, qt.IsNotNil(err), qt.Commentf("empty insns"))

	// Splitting non-empty insns without a leading Symbol is an error.
	_, err = splitSymbols(asm.Instructions{
		asm.Return(),
	})
	qt.Assert(t, qt.IsNotNil(err), qt.Commentf("insns without leading Symbol"))

	// Non-empty insns with a single Instruction that is a Symbol.
	insns := asm.Instructions{
		asm.Return().WithSymbol("sym"),
	}
	m, err := splitSymbols(insns)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("insns with a single Symbol"))

	qt.Assert(t, qt.HasLen(m, 1))
	qt.Assert(t, qt.HasLen(m["sym"], 1))

	// Insns containing duplicate Symbols.
	_, err = splitSymbols(asm.Instructions{
		asm.Return().WithSymbol("sym"),
		asm.Return().WithSymbol("sym"),
	})
	qt.Assert(t, qt.IsNotNil(err), qt.Commentf("insns containing duplicate Symbols"))

	// Insns with multiple Symbols and subprogs of various lengths.
	m, err = splitSymbols(asm.Instructions{
		asm.Return().WithSymbol("sym1"),

		asm.Mov.Imm(asm.R0, 0).WithSymbol("sym2"),
		asm.Return(),

		asm.Mov.Imm(asm.R0, 0).WithSymbol("sym3"),
		asm.Mov.Imm(asm.R0, 1),
		asm.Return(),

		asm.Mov.Imm(asm.R0, 0).WithSymbol("sym4"),
		asm.Mov.Imm(asm.R0, 1),
		asm.Mov.Imm(asm.R0, 2),
		asm.Return(),
	})
	qt.Assert(t, qt.IsNil(err), qt.Commentf("insns with multiple Symbols"))

	qt.Assert(t, qt.HasLen(m, 4))
	qt.Assert(t, qt.HasLen(m["sym1"], 1))
	qt.Assert(t, qt.HasLen(m["sym2"], 2))
	qt.Assert(t, qt.HasLen(m["sym3"], 3))
	qt.Assert(t, qt.HasLen(m["sym4"], 4))
}

func TestFlattenInstructionsAllocations(t *testing.T) {
	name := "entrypoint"
	instructions := asm.Instructions{
		asm.LoadImm(asm.R0, 0, asm.DWord),
		asm.Return(),
	}
	prog := &ProgramSpec{
		Name:         name,
		Instructions: instructions,
	}
	progs := map[string]*ProgramSpec{name: prog}
	refs := make(map[*ProgramSpec][]string)

	// ensure that flattenInstructions does not allocate memory
	// if there is no reference for the given program.
	allocs := testing.AllocsPerRun(5, func() {
		_ = flattenInstructions(name, progs, refs)
	})
	qt.Assert(t, qt.Equals(allocs, float64(0)))
}
