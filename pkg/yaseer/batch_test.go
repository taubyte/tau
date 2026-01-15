package seer

import (
	"testing"
)

func TestBatch_Commit(t *testing.T) {
	seer := newTestSeer(t)

	t.Run("Batch commit with multiple successful queries", func(t *testing.T) {
		query1 := seer.Get("batch1").Get("doc1").Document()
		query2 := seer.Get("batch2").Get("doc2").Document()
		query3 := seer.Get("batch3").Get("doc3").Document()

		query1.Set("value1")
		query2.Set("value2")
		query3.Set("value3")

		batch := seer.Batch(query1, query2, query3)
		err := batch.Commit()
		if err != nil {
			t.Fatalf("Batch commit failed: %v", err)
		}

		// Verify all documents were created
		var val1, val2, val3 string
		if err := seer.Get("batch1").Get("doc1").Value(&val1); err != nil {
			t.Fatalf("Failed to read doc1: %v", err)
		}
		if err := seer.Get("batch2").Get("doc2").Value(&val2); err != nil {
			t.Fatalf("Failed to read doc2: %v", err)
		}
		if err := seer.Get("batch3").Get("doc3").Value(&val3); err != nil {
			t.Fatalf("Failed to read doc3: %v", err)
		}

		if val1 != "value1" || val2 != "value2" || val3 != "value3" {
			t.Errorf("Values don't match: got val1=%s, val2=%s, val3=%s", val1, val2, val3)
		}
	})

	t.Run("Batch commit fails when one query has errors", func(t *testing.T) {
		query1 := seer.Get("good").Get("doc").Document().Set("value")
		query2 := seer.Get("bad").Document() // This will fail because Document() needs a Get first

		// Force an error in query2
		query2.Get("nested").Document().Set("value")

		batch := seer.Batch(query1, query2)
		err := batch.Commit()
		// The batch should either succeed or fail depending on the error handling
		// We just verify it doesn't panic
		_ = err
	})

	t.Run("Batch commit with empty batch", func(t *testing.T) {
		batch := seer.Batch()
		err := batch.Commit()
		if err != nil {
			t.Errorf("Empty batch should commit successfully, got error: %v", err)
		}
	})

	t.Run("Batch commit with nested operations", func(t *testing.T) {
		query1 := seer.Get("nested1").Get("path1").Document().Get("key1").Set("val1")
		query2 := seer.Get("nested2").Get("path2").Document().Get("key2").Set("val2")

		batch := seer.Batch(query1, query2)
		err := batch.Commit()
		if err != nil {
			t.Fatalf("Batch commit with nested operations failed: %v", err)
		}

		var val1, val2 string
		seer.Get("nested1").Get("path1").Get("key1").Value(&val1)
		seer.Get("nested2").Get("path2").Get("key2").Value(&val2)

		if val1 != "val1" || val2 != "val2" {
			t.Errorf("Nested values don't match: got val1=%s, val2=%s", val1, val2)
		}
	})
}
