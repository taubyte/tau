package storage

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	"github.com/stretchr/testify/require"
)

// realisticNames drives every encoding property: first what ds.NewKey was measured
// to mangle, then what an object store holds.
func realisticNames() []string {
	return []string{
		"a", "a/", "a//b", "a/./b", "a/../b", "..", ".", "../../etc/passwd",
		"/leading", "trailing/", "a b", "a\tb", "a\x00b", "a\nb",
		"index.html", "assets/app.9f2c.js", "photos/2024/a", "photos/2024-old",
		"photos/2024/z", "photos/2025/b", "ab", "a0", "a-b", "A", "Z", "z",
		"o", "u", "i", "v", "p", "b", "keys", "value",
		"файл", "日本語/ファイル", "emoji/🙂", strings.Repeat("x", 300),
	}
}

func randomBytesName(rng *rand.Rand) string {
	n := 1 + rng.Intn(24)
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.Intn(256))
	}
	return string(b)
}

func TestSegmentIsOneSegmentAndSurvivesCleaning(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 2000; i++ {
		id := randomBytesName(rng)
		seg := segment(id)
		require.NotContains(t, seg, "/", "an encoded identity is one segment")
		require.NotEqual(t, ".", seg)
		require.NotEqual(t, "..", seg)
		require.NotEmpty(t, seg, "a non-empty id never encodes to nothing")

		back, err := unsegment(seg)
		require.NoError(t, err)
		require.Equal(t, id, back)
	}
	require.Equal(t, "", segment(""), "and the empty id is the one case the grammar refuses at the edge")
}

func TestUnsegmentRefusesAForeignSegment(t *testing.T) {
	_, err := unsegment("not base64url!!")
	require.ErrorIs(t, err, ErrNotThisServicesRow)
}

func TestALiteralIsUnreachableFromAnEncodedIdentity(t *testing.T) {
	rng := rand.New(rand.NewSource(12))
	for i := 0; i < 5000; i++ {
		seg := segment(randomBytesName(rng))
		require.NotEqual(t, objectsSegment, seg)
		require.NotEqual(t, usageSegment, seg)
		require.NotEqual(t, instancesSegment, seg)
		require.GreaterOrEqual(t, len(seg), 2)
	}
	for _, lit := range []string{objectsSegment, usageSegment, instancesSegment} {
		require.Len(t, lit, 1, "the argument above holds only while every literal is one character")
	}
}

func TestObjectNamesRoundTripAndSurviveCleaning(t *testing.T) {
	a := addr(t)
	rng := rand.New(rand.NewSource(13))

	names := realisticNames()
	for i := 0; i < 3000; i++ {
		names = append(names, randomBytesName(rng))
	}

	for _, name := range names {
		key, err := a.versionKey(name, 42, "12D3KooWpeer")
		require.NoError(t, err, "name %q", name)
		require.True(t, clean(key), "ds.NewKey mangled %q -> %q", key, ds.NewKey(key).String())

		vk, err := ParseVersionKey(key)
		require.NoError(t, err)
		require.Equal(t, name, vk.Name)
		require.Equal(t, uint64(42), vk.Stamp)
		require.Equal(t, "12D3KooWpeer", vk.Writer)
		require.Equal(t, a.String(), vk.Address.String())
	}
}

func TestTheNamesDsNewKeyMangles(t *testing.T) {
	a := addr(t)

	rawSeen := map[string]string{}
	collisions, escapes := 0, 0
	for _, name := range realisticNames() {
		raw := ds.NewKey("/b/P/E/B/I/o/" + name).String()
		if prev, dup := rawSeen[raw]; dup {
			collisions++
			t.Logf("raw collision: %q and %q -> %s", prev, name, raw)
		}
		rawSeen[raw] = name
		if !strings.HasPrefix(raw, "/b/P/E/B/I/o/") {
			escapes++
			t.Logf("raw escape: %q -> %s", name, raw)
		}
	}
	require.Greater(t, collisions, 0, "raw splicing collides")
	require.Greater(t, escapes, 0, "raw splicing escapes the namespace")

	// And the encoded form does neither, for the same list.
	encodedSeen := map[string]string{}
	prefix, err := a.objectsPrefix()
	require.NoError(t, err)
	for _, name := range realisticNames() {
		key, err := a.versionKey(name, 1, "w")
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(key, prefix+"/"), "%q escaped: %s", name, key)
		prev, dup := encodedSeen[key]
		require.False(t, dup, "%q and %q encode to one key", prev, name)
		encodedSeen[key] = name
	}
}

func TestAnEmptyObjectNameIsRefused(t *testing.T) {
	a := addr(t)
	_, err := a.objectPrefix("")
	require.ErrorIs(t, err, ErrEmptyObjectName)
	_, err = a.versionKey("", 1, "w")
	require.ErrorIs(t, err, ErrEmptyObjectName)
	_, err = objectName("")
	require.ErrorIs(t, err, ErrNotThisServicesRow)
	_, err = objectName("zz")
	require.ErrorIs(t, err, ErrNotThisServicesRow)
}

func TestHexIsOrderAndPrefixPreserving(t *testing.T) {
	rng := rand.New(rand.NewSource(14))
	names := append(realisticNames(), "")
	for i := 0; i < 500; i++ {
		names = append(names, randomBytesName(rng))
	}

	// Order.
	raw := append([]string(nil), names...)
	sort.Strings(raw)
	byHex := append([]string(nil), names...)
	sort.Slice(byHex, func(i, j int) bool {
		return hex.EncodeToString([]byte(byHex[i])) < hex.EncodeToString([]byte(byHex[j]))
	})
	require.Equal(t, raw, byHex, "hex order must equal raw order")

	// Prefix, both directions.
	for i := 0; i < 4000; i++ {
		n := names[rng.Intn(len(names))]
		p := names[rng.Intn(len(names))]
		rawPrefix := strings.HasPrefix(n, p)
		hexPrefix := strings.HasPrefix(hex.EncodeToString([]byte(n)), hex.EncodeToString([]byte(p)))
		require.Equal(t, rawPrefix, hexPrefix, "prefix disagreement: name %q prefix %q", n, p)
	}
}

func TestBase64urlWouldLoseListOrdering(t *testing.T) {
	// base64url's alphabet is not in ASCII order: index 52 is "0" (ASCII 48)
	// and index 0 is "A" (ASCII 65), so a leading byte of 0xd0 — the first
	// byte of every Cyrillic character in UTF-8 — encodes to a character that
	// sorts BEFORE the encoding of "i". Two ordinary object names, reordered.
	require.Less(t, "index.html", "\u043f\u0440\u0438\u0432\u0435\u0442")
	require.Greater(t, segment("index.html"), segment("\u043f\u0440\u0438\u0432\u0435\u0442"))

	names := realisticNames()
	raw := append([]string(nil), names...)
	sort.Strings(raw)
	byB64 := append([]string(nil), names...)
	sort.Slice(byB64, func(i, j int) bool { return segment(byB64[i]) < segment(byB64[j]) })
	require.NotEqual(t, raw, byB64, "base64url is expected to reorder these; if it stopped, this argument needs rewriting")
}

func TestTheSuffixedPhysicalKeyWouldInvertOrderForRawNames(t *testing.T) {
	const suffix = "/v"

	// Raw: the inversion, measured.
	rawShort, rawLong := "/o/a"+suffix, "/o/a-b"+suffix
	require.Less(t, "a", "a-b", "the names are in this order")
	require.Greater(t, rawShort, rawLong, "and their physical keys are in the other one")

	// Hex: no inversion, over every pair in the corpus where one name is a
	// prefix of the other.
	names := realisticNames()
	pairs := 0
	for _, short := range names {
		for _, long := range names {
			if short == long || !strings.HasPrefix(long, short) {
				continue
			}
			pairs++
			ks := "/o/" + hex.EncodeToString([]byte(short)) + suffix
			kl := "/o/" + hex.EncodeToString([]byte(long)) + suffix
			require.Less(t, ks, kl, "hex inverted %q before %q", short, long)
		}
	}
	require.Greater(t, pairs, 0, "the corpus must contain prefix pairs for this to mean anything")
}

func TestStampSegmentIsFixedWidthAndDescending(t *testing.T) {
	stamps := []uint64{0, 1, 2, 3, 10, 100, 1 << 20, 1<<64 - 1}
	segs := make([]string, len(stamps))
	for i, s := range stamps {
		segs[i] = stampSegment(s)
		require.Len(t, segs[i], stampWidth)
		back, err := stampFromSegment(segs[i])
		require.NoError(t, err)
		require.Equal(t, s, back)
	}
	// Byte order over the segments is descending stamp order.
	sorted := append([]string(nil), segs...)
	sort.Strings(sorted)
	for i := 1; i < len(sorted); i++ {
		a, err := stampFromSegment(sorted[i-1])
		require.NoError(t, err)
		b, err := stampFromSegment(sorted[i])
		require.NoError(t, err)
		require.Greater(t, a, b, "byte order must be newest-first")
	}

	// A newer stamp is a SMALLER segment, which is the whole point.
	require.Less(t, stampSegment(10), stampSegment(2))

	// And the zero padding is what makes it true across widths. Unpadded, the
	// complement of 2^64-17 is "10" and the complement of 2^64-10 is "9", so
	// the OLDER of the two sorts first — an inversion the padding removes.
	older, newer := ^uint64(16), ^uint64(9)
	require.Less(t, older, newer)
	require.Less(t, fmt.Sprintf("%x", ^older), fmt.Sprintf("%x", ^newer), "unpadded, the older stamp sorts first")
	require.Greater(t, stampSegment(older), stampSegment(newer), "padded, the newer one does")
}

func TestStampFromSegmentRefusesAnUnpaddedOrForeignStamp(t *testing.T) {
	_, err := stampFromSegment("ff")
	require.ErrorIs(t, err, ErrNotThisServicesRow)
	_, err = stampFromSegment("zzzzzzzzzzzzzzzz")
	require.ErrorIs(t, err, ErrNotThisServicesRow)
}

func TestTheZeroAddressBuildsNoKey(t *testing.T) {
	var b Bucket
	var a Address

	require.False(t, b.Valid())
	require.False(t, a.Valid())
	require.Equal(t, "storage:<no address>", a.String())

	_, err := b.instancesPrefix()
	require.ErrorIs(t, err, ErrNoAddress)
	_, err = b.admissionKey("shop")
	require.ErrorIs(t, err, ErrNoAddress)
	_, err = b.instanceFromAdmissionKey("/b/x")
	require.ErrorIs(t, err, ErrNoAddress)
	_, err = b.Instance("shop")
	require.ErrorIs(t, err, ErrNoAddress)

	_, err = a.objectsPrefix()
	require.ErrorIs(t, err, ErrNoAddress)
	_, err = a.objectPrefix("x")
	require.ErrorIs(t, err, ErrNoAddress)
	_, err = a.versionKey("x", 1, "w")
	require.ErrorIs(t, err, ErrNoAddress)
	_, err = a.listPrefix("x")
	require.ErrorIs(t, err, ErrNoAddress)
	_, err = a.usageKey("w", usageAdded)
	require.ErrorIs(t, err, ErrNoAddress)
	_, err = a.usagePrefix()
	require.ErrorIs(t, err, ErrNoAddress)
	_, err = shardOf(b)
	require.ErrorIs(t, err, ErrNoAddress)
	_, err = instanceSegment(a)
	require.ErrorIs(t, err, ErrNoAddress)
}

func TestAnEmptyIdentityIsRefusedAtEveryDoor(t *testing.T) {
	for _, c := range []struct{ p, e, b, i string }{
		{"", "prod", "receipts", "shop"},
		{"proj", "", "receipts", "shop"},
		{"proj", "prod", "", "shop"},
		{"proj", "prod", "receipts", ""},
	} {
		_, err := NewAddress(c.p, c.e, c.b, c.i)
		require.ErrorIs(t, err, ErrEmptyIdentity, "%+v", c)
	}

	b, err := NewBucket("proj", "prod", "receipts")
	require.NoError(t, err)
	_, err = b.admissionKey("")
	require.ErrorIs(t, err, ErrEmptyIdentity)

	a := addr(t)
	_, err = a.versionKey("x", 1, "")
	require.ErrorIs(t, err, ErrEmptyIdentity)
	_, err = a.usageKey("", usageAdded)
	require.ErrorIs(t, err, ErrEmptyIdentity)
	_, err = a.usageKey("w", "nonsense")
	require.ErrorIs(t, err, ErrNotThisServicesRow)
}

func TestTwoTenantsWhoseNamesConcatenateAlikeAreTwoNamespaces(t *testing.T) {
	pairs := [][4]string{
		{"ab", "c", "d", "e"},
		{"a", "bc", "d", "e"},
		{"a", "b", "cd", "e"},
		{"a", "b", "c", "de"},
		{"a/b", "c", "d", "e"},
		{"a", "/b/c", "d", "e"},
	}
	seen := map[string][4]string{}
	for _, p := range pairs {
		a, err := NewAddress(p[0], p[1], p[2], p[3])
		require.NoError(t, err)
		key, err := a.versionKey("obj", 1, "w")
		require.NoError(t, err)
		prev, dup := seen[key]
		require.False(t, dup, "%v and %v share a key", prev, p)
		seen[key] = p

		// And every one of them is exactly nine segments deep, whatever the
		// identities were.
		require.Len(t, strings.Split(strings.TrimPrefix(key, "/"), "/"), 9)
	}
}

func TestAnInstanceCannotFileRowsInTheAdmissionNamespace(t *testing.T) {
	b, err := NewBucket("proj", "prod", "receipts")
	require.NoError(t, err)
	admission, err := b.instancesPrefix()
	require.NoError(t, err)

	for _, name := range []string{"i", "i/x", "../i/x", "i/../i", instancesSegment} {
		a, err := b.Instance(name)
		require.NoError(t, err)
		key, err := a.versionKey("obj", 1, "w")
		require.NoError(t, err)
		require.False(t, strings.HasPrefix(key, admission),
			"instance %q filed a row under the admission namespace: %s", name, key)
		require.True(t, clean(key))
	}
}

func TestAdmissionRowsRoundTrip(t *testing.T) {
	b, err := NewBucket("proj", "prod", "receipts")
	require.NoError(t, err)
	for _, inst := range []string{"shop", "shop/2", "..", "a b"} {
		key, err := b.admissionKey(inst)
		require.NoError(t, err)
		require.True(t, clean(key))
		got, err := b.instanceFromAdmissionKey(key)
		require.NoError(t, err)
		require.Equal(t, inst, got)
	}

	_, err = b.instanceFromAdmissionKey("/b/nope")
	require.ErrorIs(t, err, ErrNotThisServicesRow)
	prefix, err := b.instancesPrefix()
	require.NoError(t, err)
	_, err = b.instanceFromAdmissionKey(prefix + "a/b")
	require.ErrorIs(t, err, ErrNotThisServicesRow)
}

func TestParseVersionKeyRefusesEverythingItDidNotWrite(t *testing.T) {
	a := addr(t)
	good, err := a.versionKey("obj", 7, "peer")
	require.NoError(t, err)
	_, err = ParseVersionKey(good)
	require.NoError(t, err)

	parts := strings.Split(strings.TrimPrefix(good, "/"), "/")
	bad := []string{
		"/b/a/b/c",                           // too shallow
		good + "/extra",                      // too deep
		"/x/" + strings.Join(parts[1:], "/"), // wrong root
		"/b/" + strings.Join(parts[1:5], "/") + "/z/" + strings.Join(parts[6:], "/"),  // wrong literal
		"/b/!!/" + strings.Join(parts[2:], "/"),                                       // project not base64url
		"/b/" + parts[1] + "/!!/" + strings.Join(parts[3:], "/"),                      // environment not base64url
		"/b/" + strings.Join(parts[1:3], "/") + "/!!/" + strings.Join(parts[4:], "/"), // bucket
		"/b/" + strings.Join(parts[1:4], "/") + "/!!/" + strings.Join(parts[5:], "/"), // instance
		"/b/" + strings.Join(parts[1:6], "/") + "/zz/" + strings.Join(parts[7:], "/"), // name not hex
		"/b/" + strings.Join(parts[1:7], "/") + "/ff/" + parts[8],                     // stamp not padded
		"/b/" + strings.Join(parts[1:8], "/") + "/!!",                                 // writer not base64url
	}
	for _, k := range bad {
		_, err := ParseVersionKey(k)
		require.ErrorIs(t, err, ErrNotThisServicesRow, "accepted %q", k)
	}

	// An empty identity segment is refused too, and it is the only one that
	// reaches the Address constructor rather than the decoder.
	empty := "/b//" + strings.Join(parts[2:], "/")
	_, err = ParseVersionKey(empty)
	require.ErrorIs(t, err, ErrNotThisServicesRow)
}

func TestListPrefixIsARawBytePrefixAndTheEmptyOneSelectsEverything(t *testing.T) {
	a := addr(t)
	objects, err := a.objectsPrefix()
	require.NoError(t, err)

	all, err := a.listPrefix("")
	require.NoError(t, err)
	require.Equal(t, objects+"/", all)

	p, err := a.listPrefix("photos/2024")
	require.NoError(t, err)
	require.Equal(t, objects+"/"+hex.EncodeToString([]byte("photos/2024")), p)

	for _, name := range []string{"photos/2024/a", "photos/2024-old", "photos/2024/z"} {
		k, err := a.versionKey(name, 1, "w")
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(k, p), "%q should match", name)
	}
	for _, name := range []string{"photos/2025/b", "photo", "qhotos/2024/a"} {
		k, err := a.versionKey(name, 1, "w")
		require.NoError(t, err)
		require.False(t, strings.HasPrefix(k, p), "%q should not match", name)
	}
}

func TestAListPrefixCannotBeAQueryNamespace(t *testing.T) {
	a := addr(t)
	p, err := a.listPrefix("photos/2024")
	require.NoError(t, err)

	store := newOrderedMem()
	ctx := context.Background()
	for _, name := range []string{"photos/2024/a", "photos/2024-old", "photos/2025/b"} {
		k, err := a.versionKey(name, 1, "w")
		require.NoError(t, err)
		require.NoError(t, store.Put(ctx, ds.RawKey(k), []byte("x")))
	}

	// As a namespace: nothing, because "/" is appended.
	res, err := store.Query(ctx, query.Query{Prefix: p})
	require.NoError(t, err)
	rest, err := res.Rest()
	require.NoError(t, err)
	require.Empty(t, rest, "a partial segment cannot be a query namespace")

	// As a raw filter: exactly the two.
	objects, err := a.objectsPrefix()
	require.NoError(t, err)
	res, err = store.Query(ctx, query.Query{Prefix: objects, Filters: []query.Filter{query.FilterKeyPrefix{Prefix: p}}})
	require.NoError(t, err)
	rest, err = res.Rest()
	require.NoError(t, err)
	require.Len(t, rest, 2)
}

func TestUsageKeysAreOneWriterEach(t *testing.T) {
	a := addr(t)
	prefix, err := a.usagePrefix()
	require.NoError(t, err)
	seen := map[string]struct{}{}
	for _, w := range []string{"peerA", "peerB", "peer/A", ".."} {
		for _, kind := range []string{usageAdded, usageRemoved, usageCheckpoint} {
			k, err := a.usageKey(w, kind)
			require.NoError(t, err)
			require.True(t, clean(k))
			require.True(t, strings.HasPrefix(k, prefix+"/"))
			_, dup := seen[k]
			require.False(t, dup, "usage key collision at %s", k)
			seen[k] = struct{}{}
			require.Len(t, strings.Split(strings.TrimPrefix(k, prefix+"/"), "/"), 2)
		}
	}
}

func TestTheMapStoreIsUnorderedAndTheDoubleIsNot(t *testing.T) {
	ctx := context.Background()
	a := addr(t)

	write := func(store Datastore) {
		for _, s := range []uint64{1, 2, 3, 10, 100} {
			k, err := a.versionKey("photo.jpg", s, "peerA")
			require.NoError(t, err)
			require.NoError(t, store.Put(ctx, ds.RawKey(k), []byte(fmt.Sprint(s))))
		}
	}
	unorderedRun := func(store Datastore) []string {
		res, err := store.Query(ctx, query.Query{Prefix: mustObjectPrefix(t, a, "photo.jpg")})
		require.NoError(t, err)
		defer res.Close()
		rest, err := res.Rest()
		require.NoError(t, err)
		out := make([]string, 0, len(rest))
		for _, e := range rest {
			out = append(out, string(e.Value))
		}
		return out
	}

	sortedWant := []string{"100", "10", "3", "2", "1"}

	om := newOrderedMem()
	write(om)
	require.Equal(t, sortedWant, unorderedRun(om), "the double must be ordered")

	um := newUnorderedMem()
	write(um)
	// Enough runs for Go's randomised map order to show.
	disagreed := false
	for i := 0; i < 200 && !disagreed; i++ {
		if !equalStrings(unorderedRun(um), sortedWant) {
			disagreed = true
		}
	}
	require.True(t, disagreed, "ds.MapDatastore is expected to return rows in arbitrary order")

	// And the resolution door is right on it anyway, because it names the order.
	idx := index{store: um}
	v, err := idxCurrentRaw(t, idx, a, "photo.jpg")
	require.NoError(t, err)
	require.Equal(t, uint64(100), v)
}

func mustObjectPrefix(t *testing.T, a Address, name string) string {
	t.Helper()
	p, err := a.objectPrefix(name)
	require.NoError(t, err)
	return p
}

// idxCurrentRaw reads the first row's stamp without going through the record
// codec, so it can be used against rows written as plain bytes.
func idxCurrentRaw(t *testing.T, i index, a Address, name string) (uint64, error) {
	t.Helper()
	prefix, err := a.objectPrefix(name)
	require.NoError(t, err)
	r, ok, err := i.first(context.Background(), prefix)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, ErrNotFound
	}
	vk, err := ParseVersionKey(r.key)
	if err != nil {
		return 0, err
	}
	return vk.Stamp, nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
