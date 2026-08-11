//go:build windows && amd64

package native

// R2-2 W^X deep verification, Windows amd64 portion.
//
// Verification is done through the Windows memory-protection metadata, never
// through package internals:
//
//   - VirtualQuery(code.Entry()) returns the MemoryBasicInformation of the
//     committed region; live protection must be PAGE_EXECUTE_READ. The
//     AllocationProtect field records the protection at allocation time
//     (PAGE_READWRITE), which proves the write-then-flip publish protocol:
//     memory is allocated writable, the machine code is written, and only
//     then is the protection flipped to execute-read. At no point is a
//     published page executable while still writable.
//   - wxNoRWXPages walks the entire user virtual address space with
//     VirtualQuery (advancing base+RegionSize per region, so the number of
//     calls equals the number of regions, not the address range) and fails on
//     any committed page whose protection is simultaneously executable and
//     writable (PAGE_EXECUTE_READWRITE or PAGE_EXECUTE_WRITECOPY).
//   - wxRegionGone queries the old entry address after Close: a released
//     allocation no longer resolves to a committed region (State != MEM_COMMIT).

import (
	"fmt"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// wxRegionIsRX reports whether the committed region containing entry is
// readable and executable and not writable.
func wxRegionIsRX(entry uintptr) error {
	var info windows.MemoryBasicInformation
	if err := windows.VirtualQuery(entry, &info, unsafe.Sizeof(info)); err != nil {
		return fmt.Errorf("VirtualQuery(%#x): %w", entry, err)
	}
	if info.State != windows.MEM_COMMIT {
		return fmt.Errorf("region at %#x is not committed (state=%#x)", entry, info.State)
	}
	// Windows protection codes are single enum values, not bit compositions:
	// the executable family is 0x10/0x20/0x40/0x80 (top nibble), the writable
	// family is 0x04/0x08/0x40/0x80.
	if info.Protect&0xF0 == 0 {
		return fmt.Errorf("region at %#x is not executable (protect=%#x)", entry, info.Protect)
	}
	if info.Protect&(windows.PAGE_READWRITE|windows.PAGE_WRITECOPY|
		windows.PAGE_EXECUTE_READWRITE|windows.PAGE_EXECUTE_WRITECOPY) != 0 {
		return fmt.Errorf("region at %#x is writable (protect=%#x)", entry, info.Protect)
	}
	return nil
}

// wxRegionGone reports whether no committed region contains entry anymore
// (after the allocation was released).
func wxRegionGone(entry uintptr) (bool, error) {
	if entry == 0 {
		return true, nil
	}
	var info windows.MemoryBasicInformation
	if err := windows.VirtualQuery(entry, &info, unsafe.Sizeof(info)); err != nil {
		return false, fmt.Errorf("VirtualQuery(%#x): %w", entry, err)
	}
	return info.State != windows.MEM_COMMIT, nil
}

// wxNoRWXPages walks the whole user virtual address space and fails if any
// committed page is both executable and writable.
func wxNoRWXPages() error {
	var addr uintptr
	regions := 0
	for {
		var info windows.MemoryBasicInformation
		if err := windows.VirtualQuery(addr, &info, unsafe.Sizeof(info)); err != nil {
			// Reached the end of the accessible user address space.
			return nil
		}
		if info.State == windows.MEM_COMMIT {
			if info.Protect&(windows.PAGE_EXECUTE_READWRITE|windows.PAGE_EXECUTE_WRITECOPY) != 0 {
				return fmt.Errorf("RWX page found: base=%#x size=%#x protect=%#x type=%#x",
					info.BaseAddress, info.RegionSize, info.Protect, info.Type)
			}
		}
		next := info.BaseAddress + info.RegionSize
		if next <= addr {
			return fmt.Errorf("VirtualQuery walk stalled at %#x (region size %#x)", addr, info.RegionSize)
		}
		addr = next
		regions++
		if regions > 1<<20 {
			return fmt.Errorf("VirtualQuery walk exceeded region budget (%d regions)", regions)
		}
	}
}

// TestExecMemWXWindowsProtectionIsExecuteRead pins the exact protection of a
// published region: live PAGE_EXECUTE_READ (r-x, not writable) over an
// allocation originally created PAGE_READWRITE (the write-then-flip protocol).
func TestExecMemWXWindowsProtectionIsExecuteRead(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	code, err := Publish(AddF64Kernel())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = code.Close()
		requireExecutableBaseline(t, baseRegions, baseBytes)
	}()

	var info windows.MemoryBasicInformation
	if err := windows.VirtualQuery(code.Entry(), &info, unsafe.Sizeof(info)); err != nil {
		t.Fatal(err)
	}
	if info.State != windows.MEM_COMMIT {
		t.Fatalf("state = %#x, want MEM_COMMIT", info.State)
	}
	if info.Protect != windows.PAGE_EXECUTE_READ {
		t.Fatalf("live protect = %#x, want PAGE_EXECUTE_READ (%#x)", info.Protect, windows.PAGE_EXECUTE_READ)
	}
	if info.AllocationProtect != windows.PAGE_READWRITE {
		t.Fatalf("allocation protect = %#x, want PAGE_READWRITE (%#x): publish protocol must allocate writable, then flip to RX",
			info.AllocationProtect, windows.PAGE_READWRITE)
	}
	requireKernelSumBitExact(t, code, 0.1, 0.2)
	requireNoRWXPages(t)
}

// TestExecMemWXWindowsMultiPageRegionSize publishes code larger than one page
// and verifies the committed region actually spans multiple pages.
func TestExecMemWXWindowsMultiPageRegionSize(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	code, err := Publish(paddedKernel(2*4096 + 17))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = code.Close()
		requireExecutableBaseline(t, baseRegions, baseBytes)
	}()

	var info windows.MemoryBasicInformation
	if err := windows.VirtualQuery(code.Entry(), &info, unsafe.Sizeof(info)); err != nil {
		t.Fatal(err)
	}
	if info.RegionSize < 2*4096 {
		t.Fatalf("committed region size = %#x, want >= 2 pages", info.RegionSize)
	}
	if info.Protect != windows.PAGE_EXECUTE_READ {
		t.Fatalf("live protect = %#x, want PAGE_EXECUTE_READ for the whole multi-page region", info.Protect)
	}
	requireKernelSumBitExact(t, code, 3, 4)
}
