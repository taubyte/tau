package seer

import (
	"testing"

	"golang.org/x/exp/slices"
	"gotest.tools/v3/assert"
)

func TestListEmpty(t *testing.T) {
	seer, err := New(fixtureFS(true, "/"))
	assert.NilError(t, err)

	items, err := seer.List()
	assert.NilError(t, err)
	assert.Assert(t, len(items) == 0)

	for _, query := range []*Query{
		seer.Get("parent"),
		seer.Get("parent").Get("p").Document(),
		seer.Get("a").Get("b").Get("C").Document().Get("a").Get("orange"),
	} {
		err = query.Fork().Commit()
		assert.NilError(t, err)

		items, err = query.Fork().List()
		assert.NilError(t, err)
		assert.Assert(t, len(items) == 0)
	}
}

func TestListSet(t *testing.T) {
	seer, err := New(fixtureFS(true, "/"))
	assert.NilError(t, err)

	seer.Get("parent").Get("p").Commit()
	listItems, err := seer.List()
	assert.NilError(t, err)
	assert.Equal(t, listItems[0], string("parent"))

	listItems, err = seer.Get("parent").List()
	assert.NilError(t, err)
	assert.Equal(t, listItems[0], "p")
}

func TestListMultiSet(t *testing.T) {
	seer, err := New(fixtureFS(true, "/"))
	assert.NilError(t, err)

	items := []string{"oranges", "bananas", "pears", "pineapples", "coconuts"}
	for _, i := range items {
		err = seer.Get("parent").Get(i).Commit()
		assert.NilError(t, err)
	}

	listItems, err := seer.Get("parent").List()
	assert.NilError(t, err)

	assertContains(t, listItems, items...)
}

func TestListDeepMultiSet(t *testing.T) {
	seer, err := New(fixtureFS(true, "/"))
	assert.NilError(t, err)

	items := []string{"oranges", "bananas", "pears", "pineapples", "coconuts"}
	for _, i := range items {
		assert.NilError(t, seer.Get("parent").Get("sad").Get("fruits").Get(i).Commit())
	}

	listItems, err := seer.Get("parent").Get("sad").Get("fruits").List()
	assert.NilError(t, err)

	assertContains(t, listItems, items...)
}

func TestListDeepCommitDelete(t *testing.T) {
	seer, err := New(fixtureFS(true, "/"))
	assert.NilError(t, err)

	items := []string{"oranges", "bananas", "pears", "pineapples", "coconuts"}
	toDelete := []string{items[1], items[2]}
	expectedItems := append(items[:1], items[3:]...)

	query := seer.Get("parent").Get("sad").Get("fruits")

	for _, i := range items {
		err = query.Fork().Get(i).Commit()
		assert.NilError(t, err)
	}

	for _, i := range toDelete {
		err = query.Fork().Get(i).Delete().Commit()
		assert.NilError(t, err)
	}

	listItems, err := query.List()
	assert.NilError(t, err)

	assertContains(t, listItems, expectedItems...)
	assertNotContains(t, listItems, toDelete...)

}

func TestListOnDocument(t *testing.T) {
	seer, err := New(fixtureFS(true, "/"))
	assert.NilError(t, err)

	documentName := "some-doc"
	item1 := "pears"
	item2 := "oranges"

	err = seer.Get(documentName).Document().Get(item1).Set(10).Commit()
	assert.NilError(t, err)

	err = seer.Get(documentName).Document().Get(item2).Set(20).Commit()
	assert.NilError(t, err)

	list, err := seer.Get(documentName).List()
	assert.NilError(t, err)

	assertContains(t, list, item1, item2)
}

func assertContains(t *testing.T, val []string, items ...string) {
	for _, item := range items {
		assert.Assert(t, slices.Contains(val, item), "%s not in %v", item, val)
	}
}

func assertNotContains(t *testing.T, val []string, items ...string) {
	for _, item := range items {
		assert.Assert(t, slices.Contains(val, item) == false, "%s in %v", item, val)
	}
}
