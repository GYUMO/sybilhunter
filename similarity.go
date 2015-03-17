// Computes similarity between router descriptors.

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	tor "git.torproject.org/user/phw/zoossh.git"
	levenshtein "github.com/arbovm/levenshtein"
)

// hasDefaultExitPolicy returns true if the given descriptor's reject policy is
// the default reject policy.
func hasDefaultExitPolicy(desc *tor.RouterDescriptor) bool {

	defaultReject1 := "0.0.0.0/8:* 169.254.0.0/16:* 127.0.0.0/8:* " +
		"192.168.0.0/16:* 10.0.0.0/8:* 172.16.0.0/12:* "
	defaultReject2 := ":* *:25 *:119 *:135-139 *:445 *:563 *:1214 " +
		"*:4661-4666 *:6346-6429 *:6699 *:6881-6999"
	defaultReject := defaultReject1 + desc.Address.String() + defaultReject2

	return strings.TrimSpace(desc.RawReject) == defaultReject
}

// Represents our similarity vector, i.e., the difference between two router
// descriptors.
type DescriptorSimilarity struct {
	Fingerprint1 string
	Fingerprint2 string

	UptimeDiff      uint64
	BandwidthDiff   uint64
	ORPortDiff      uint16
	SharedFprPrefix uint32
	LevenshteinDist int

	SameFamily  bool
	SameAddress bool
	SameContact bool
	SameVersion bool
	HaveDirPort bool
	SamePolicy  bool
}

// String implements the Stringer interface for pretty printing.
func (s DescriptorSimilarity) String() string {

	return fmt.Sprintf("%s, %s, %t, %t, %t, %t, %t, %d, %d, %d, %d, %d",
		s.Fingerprint1,
		s.Fingerprint2,
		s.SameContact,
		s.SameFamily,
		s.SameAddress,
		s.SameVersion,
		s.SamePolicy,
		s.UptimeDiff,
		s.ORPortDiff,
		s.BandwidthDiff,
		s.SharedFprPrefix,
		s.LevenshteinDist)
}

// CalcDescSimilarity determines the similarity between the two given relay
// descriptors.  The similarity is a vector of numbers, which is returned.
func CalcDescSimilarity(desc1, desc2 *tor.RouterDescriptor) *DescriptorSimilarity {

	similarity := new(DescriptorSimilarity)

	similarity.Fingerprint1 = desc1.Fingerprint
	similarity.Fingerprint2 = desc2.Fingerprint

	similarity.UptimeDiff = MaxUInt64(desc1.Uptime, desc2.Uptime) -
		MinUInt64(desc1.Uptime, desc2.Uptime)
	similarity.BandwidthDiff = MaxUInt64(desc1.BandwidthAvg, desc2.BandwidthAvg) -
		MinUInt64(desc1.BandwidthAvg, desc2.BandwidthAvg)
	similarity.ORPortDiff = MaxUInt16(desc1.ORPort, desc2.ORPort) -
		MinUInt16(desc1.ORPort, desc2.ORPort)

	// We compare hex-encoded fingerprints, so we have a granularity of four
	// bits.  For example, the following two fingerprints have a shared prefix
	// of five:
	//   2C23B 21BEA DFB95 6247F  6DA97 36A61 EDCE9 48413
	//   2C23B 41049 6F573 A616B  FF37B C12A2 B39F2 DBE5E
	similarity.SharedFprPrefix = 0
	for i := 0; i < 40; i++ {
		if desc1.Fingerprint[i] != desc2.Fingerprint[i] {
			break
		}
		similarity.SharedFprPrefix++
	}

	similarity.LevenshteinDist = levenshtein.Distance(desc1.Nickname, desc2.Nickname)

	similarity.SameFamily = desc1.HasFamily(desc2.Fingerprint) && desc2.HasFamily(desc1.Fingerprint)
	similarity.SameAddress = (desc1.Address.String() == desc2.Address.String())
	similarity.SameContact = (desc1.Contact == desc2.Contact) && desc1.Contact != ""
	similarity.SameVersion = (desc1.TorVersion == desc2.TorVersion)
	similarity.HaveDirPort = (desc1.DirPort != 0) && (desc2.DirPort != 0)

	// We don't care about the default or the universal reject policy.
	if !hasDefaultExitPolicy(desc1) && strings.TrimSpace(desc1.RawReject) != "*:*" {
		similarity.SamePolicy = desc1.RawReject == desc2.RawReject
	}

	return similarity
}

// PairwiseSimilarities computes pairwise similarities between the given relay
// descriptors.  All similarities, approximately n^2/2, are written to stdout
// as comma-separated values.
func PairwiseSimilarities(descs *tor.RouterDescriptors) {

	// Turn the map keys (i.e., the relays' fingerprints) into a list.
	size := len(descs.RouterDescriptors)
	fprs := make([]string, size)

	i := 0
	for fpr, _ := range descs.RouterDescriptors {
		fprs[i] = fpr
		i++
	}

	// Compute pairwise relay similarities.  This takes O(n^2/2) operations.
	for i := 0; i < size; i++ {

		fpr1 := fprs[i]
		for j := i + 1; j < size; j++ {

			fpr2 := fprs[j]
			desc1, _ := descs.Get(fpr1)
			desc2, _ := descs.Get(fpr2)

			fmt.Println(CalcDescSimilarity(desc1, desc2))
		}
	}
}

// extractObjects attempts to parse the given, unknown file and returns a
// collection of objects.  It's up to the caller to convert the returned
// interface type to something more useful.
func extractObjects(path string, info os.FileInfo) (tor.ObjectCollector, error) {

	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("File \"%s\" does not exist.", path)
	}

	if info.IsDir() {
		return nil, fmt.Errorf("\"%s\" is a directory.", path)
	}

	objects, err := tor.ParseUnknownFile(path)
	if err != nil {
		return nil, err
	}

	return objects, nil
}

// accumulateDescriptors returns a walking function that accumulates the router
// descriptors of all encountered files.
func accumulateDescriptors(descs *tor.RouterDescriptors) filepath.WalkFunc {

	return func(path string, info os.FileInfo, err error) error {

		if err != nil {
			log.Println(err)
			return nil
		}

		objects, err := extractObjects(path, info)
		if err != nil {
			log.Println(err)
			return nil
		}

		switch v := objects.(type) {
		case *tor.RouterDescriptors:
			for fpr, getVal := range v.RouterDescriptors {
				descs.Set(fpr, getVal())
			}
		default:
			log.Printf("File format of \"%s\" not yet supported.\n", path)
		}

		return nil
	}
}

// processDescriptors attempts to parse the given file and compute all pairwise
// similarities if parsing succeeded.
func processDescriptors(path string, info os.FileInfo, err error) error {

	objects, err := extractObjects(path, info)
	if err != nil {
		log.Println(err)
		return nil
	}

	switch v := objects.(type) {
	case *tor.RouterDescriptors:
		PairwiseSimilarities(v)
	default:
		log.Printf("File format of \"%s\" not yet supported.\n", path)
	}

	return nil
}

// AnalyseSimilarities walks the given file or directory and computes pairwise
// relay similarities.  If the cumulative argument is set to true, the content
// of all files is accumulated rather than analysed independently.
func AnalyseSimilarities(path string, cumulative bool) {

	if cumulative {
		log.Println("Processing files cumulatively.")
		descs := tor.NewRouterDescriptors()
		filepath.Walk(path, accumulateDescriptors(descs))
		PairwiseSimilarities(descs)
	} else {
		log.Println("Processing files independently.")
		filepath.Walk(path, processDescriptors)
	}
}
