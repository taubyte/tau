package register

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAHostReadDependsOnItsOneKeyFoundOrNot(t *testing.T) {
	r, _ := newRegister(t)
	publish(t, r, "shop", "prod", 1, "shop.example.com")

	read, d := r.Recorded()
	_, _, err := read.ResolveHost(ctx, "Shop.Example.com.")
	require.NoError(t, err, "a trailing dot and capitals are the same name")
	require.Equal(t, Depends{Keys: []string{"h/shop.example.com"}}, *d)

	read, d = r.Recorded()
	_, _, err = read.ResolveHost(ctx, "nobody.example.com")
	require.ErrorIs(t, err, ErrNoHost)
	require.Equal(t, []string{"h/nobody.example.com"}, d.Keys, "a miss depends on the key that is not there")
	require.True(t, d.Covers("/h/nobody.example.com"), "and its claim, written later, is a change it covers")
	require.False(t, d.Covers("h/nobody.example.com.evil"))
}

func TestAViewDependsOnItsRevisionsAndTheObjectsItLookedFor(t *testing.T) {
	r, _ := newRegister(t)
	_, err := r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("shop"),
		Views: []View{{Destination: "region-1", Compiled: []byte("shop region-1")}}})
	require.NoError(t, err)

	read, d := r.Recorded()
	v, err := read.ResolveView(ctx, "shop", "prod", "region-1")
	require.NoError(t, err)
	require.Contains(t, d.Prefixes, "m/shop/prod/")
	require.True(t, d.Covers(objectKey(v.Object)), "the view's object was looked up, so its arrival is covered")
	require.True(t, d.Covers(objectKey(v.Manifest)))

	require.False(t, d.Covers("m/blog/prod/"+revisionSegment(1)+"/x"), "another project's revisions are not")
	require.True(t, d.Covers("m/shop/prod/"+revisionSegment(2)+"/x"), "a new revision of this one is")
}

func TestASecondReadThatTouchesNothingNewIsWithinTheFirst(t *testing.T) {
	held := Depends{Keys: []string{"h/a", "o/1"}, Prefixes: []string{"m/shop/prod/"}}
	require.True(t, Depends{Keys: []string{"o/1", "m/shop/prod/r/x"}}.Within(held), "a key under a held prefix is held")
	require.True(t, Depends{Prefixes: []string{"m/shop/prod/"}}.Within(held))
	require.False(t, Depends{Keys: []string{"o/2"}}.Within(held))
	require.False(t, Depends{Prefixes: []string{"m/blog/prod/"}}.Within(held))
}

func TestARecordedReadWritesWhatAWriteWould(t *testing.T) {
	r, _ := newRegister(t)
	write, d := r.Recorded()
	publish(t, write, "shop", "prod", 1, "shop.example.com")
	project, _, err := r.ResolveHost(ctx, "shop.example.com")
	require.NoError(t, err, "the note watches reads and leaves writes as they are")
	require.Equal(t, "shop", project)
	require.NotEmpty(t, d.Keys)
}
