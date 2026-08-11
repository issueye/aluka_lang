//go:build linux && amd64

package native

// R2-2 W^X deep verification, Linux amd64 portion.
//
// Verification is done through the OS memory-mapping metadata of the current
// process, never through package internals:
//
//   - /proc/self/maps is parsed (address range, perms) for every mapping.
//     The mapping containing code.Entry() must have perms "r-xp" (readable +
//     executable, not writable) after publish.
//   - wxNoRWXPages fails on any /proc/self/maps line whose perms contain both
//     'w' and 'x' (a W^X violation anywhere in the process).
//   - wxRegionGone reports whether no mapping contains the old entry address
//     anymore (munmap removed it from /proc/self/maps).

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

type linuxMapping struct {
	start, end uint64
	perms      string
	path       string
}

// readLinuxMappings parses /proc/self/maps into sorted address ranges.
func readLinuxMappings() ([]linuxMapping, error) {
	f, err := os.Open("/proc/self/maps")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []linuxMapping
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		parts := strings.SplitN(fields[0], "-", 2)
		if len(parts) != 2 {
			continue
		}
		start, startErr := strconv.ParseUint(parts[0], 16, 64)
		end, endErr := strconv.ParseUint(parts[1], 16, 64)
		if startErr != nil || endErr != nil {
			continue
		}
		path := ""
		if len(fields) >= 6 {
			path = fields[5]
		}
		out = append(out, linuxMapping{start: start, end: end, perms: fields[1], path: path})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// mappingFor returns the mapping containing addr, or nil.
func mappingFor(maps []linuxMapping, addr uintptr) *linuxMapping {
	for i := range maps {
		m := &maps[i]
		if uint64(addr) >= m.start && uint64(addr) < m.end {
			return m
		}
	}
	return nil
}

// wxRegionIsRX reports whether the mapping containing entry is readable and
// executable and not writable.
func wxRegionIsRX(entry uintptr) error {
	maps, err := readLinuxMappings()
	if err != nil {
		return err
	}
	m := mappingFor(maps, entry)
	if m == nil {
		return fmt.Errorf("no /proc/self/maps mapping contains entry %#x", entry)
	}
	if !strings.Contains(m.perms, "x") || strings.Contains(m.perms, "w") {
		return fmt.Errorf("mapping %#x-%#x perms %q containing entry %#x is not RX",
			m.start, m.end, m.perms, entry)
	}
	return nil
}

// wxRegionGone reports whether no mapping contains entry anymore (after
// munmap).
func wxRegionGone(entry uintptr) (bool, error) {
	if entry == 0 {
		return true, nil
	}
	maps, err := readLinuxMappings()
	if err != nil {
		return false, err
	}
	return mappingFor(maps, entry) == nil, nil
}

// wxNoRWXPages fails if any mapping in /proc/self/maps is both writable and
// executable.
func wxNoRWXPages() error {
	maps, err := readLinuxMappings()
	if err != nil {
		return err
	}
	for _, m := range maps {
		if strings.Contains(m.perms, "w") && strings.Contains(m.perms, "x") {
			return fmt.Errorf("RWX mapping found: %#x-%#x perms %q path %q",
				m.start, m.end, m.perms, m.path)
		}
	}
	return nil
}

// TestExecMemWXLinuxMappingPermsIsRX pins the exact /proc/self/maps perms of a
// published region: "r-xp" (readable + executable, private, not writable).
func TestExecMemWXLinuxMappingPermsIsRX(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	code, err := Publish(AddF64Kernel())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = code.Close()
		requireExecutableBaseline(t, baseRegions, baseBytes)
	}()

	maps, err := readLinuxMappings()
	if err != nil {
		t.Fatal(err)
	}
	m := mappingFor(maps, code.Entry())
	if m == nil {
		t.Fatal("published region not found in /proc/self/maps")
	}
	if m.perms != "r-xp" {
		t.Fatalf("mapping perms = %q, want \"r-xp\" (mapping %#x-%#x)", m.perms, m.start, m.end)
	}
	// Independent cross-check through the parser used by the existing
	// native_linux_test.go helper.
	if perms, err := linuxMappingPermissions(code.Entry()); err != nil || perms != m.perms {
		t.Fatalf("cross-check perms = %q err = %v, want %q", perms, err, m.perms)
	}
	requireKernelSumBitExact(t, code, 1.25, 2.5)
	requireNoRWXPages(t)
}

// TestExecMemWXLinuxMultiPageMapping publishes code larger than one page and
// verifies the mapping actually spans multiple pages, entirely RX.
func TestExecMemWXLinuxMultiPageMapping(t *testing.T) {
	baseRegions, baseBytes := LiveExecutableMemory()
	code, err := Publish(paddedKernel(2*4096 + 17))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = code.Close()
		requireExecutableBaseline(t, baseRegions, baseBytes)
	}()

	maps, err := readLinuxMappings()
	if err != nil {
		t.Fatal(err)
	}
	m := mappingFor(maps, code.Entry())
	if m == nil {
		t.Fatal("published region not found in /proc/self/maps")
	}
	if m.end-m.start < 2*4096 {
		t.Fatalf("mapping size = %d bytes, want >= 2 pages", m.end-m.start)
	}
	if strings.Contains(m.perms, "w") {
		t.Fatalf("mapping %#x-%#x perms %q must not be writable", m.start, m.end, m.perms)
	}
	requireKernelSumBitExact(t, code, 3, 4)
}
