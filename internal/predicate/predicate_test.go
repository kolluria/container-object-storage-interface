/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package predicate

import (
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	cosiapi "sigs.k8s.io/container-object-storage-interface/client/apis/objectstorage/v1alpha2"
)

func Test_toTypedOrLogError(t *testing.T) {
	// ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	// logger := ctrl.Log.WithName("predicate")
	logger := logr.Discard() // comment this and uncomment above to locally test log messages

	scheme := runtime.NewScheme()
	err := cosiapi.AddToScheme(scheme)
	require.NoError(t, err)

	t.Run("matching type", func(t *testing.T) {
		access := &cosiapi.BucketAccess{
			ObjectMeta: meta.ObjectMeta{
				Namespace: "ns",
				Name:      "name",
			},
		}
		accessObj := client.Object(access)

		gotObj, ok := toTypedOrLogError[*cosiapi.BucketAccess](logger, scheme, accessObj)
		assert.Equal(t, access, gotObj)
		assert.True(t, ok)
	})

	t.Run("nonmatching type", func(t *testing.T) {
		claim := &cosiapi.BucketClaim{
			ObjectMeta: meta.ObjectMeta{
				Namespace: "ns",
				Name:      "name",
			},
		}
		claimObj := client.Object(claim)

		gotObj, ok := toTypedOrLogError[*cosiapi.BucketAccess](logger, scheme, claimObj)
		assert.Empty(t, gotObj)
		assert.False(t, ok)
	})
}

func TestDeletionTimestampAdded(t *testing.T) {
	deletionTimestamp := meta.Now()
	old := &cosiapi.BucketAccess{}
	deleting := old.DeepCopy()
	deleting.DeletionTimestamp = &deletionTimestamp

	predicate := DeletionTimestampAdded()

	assert.True(t, predicate.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: deleting}))
	assert.False(t, predicate.Update(event.UpdateEvent{ObjectOld: deleting, ObjectNew: deleting.DeepCopy()}))
}

func TestBucketClaimBeingDeletedAnnotationAdded(t *testing.T) {
	old := &cosiapi.Bucket{}
	annotated := old.DeepCopy()
	annotated.Annotations = map[string]string{cosiapi.BucketClaimBeingDeletedAnnotation: ""}

	predicate := BucketClaimBeingDeletedAnnotationAdded()

	assert.True(t, predicate.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: annotated}))
	assert.False(t, predicate.Update(event.UpdateEvent{ObjectOld: annotated, ObjectNew: annotated.DeepCopy()}))
	assert.False(t, predicate.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: old.DeepCopy()}))
	assert.False(t, predicate.Update(event.UpdateEvent{ObjectOld: annotated, ObjectNew: old}))
}

func Test_handoffOccurred(t *testing.T) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	logger := ctrl.Log.WithName("predicate")
	// logger := logr.Discard() // comment this and uncomment above to locally test log messages

	t.Run("no handoff", func(t *testing.T) {
		old := &cosiapi.BucketAccess{}
		new := &cosiapi.BucketAccess{}

		assert.False(t, handoffOccurred(logger, old, new))
	})

	t.Run("handoff", func(t *testing.T) {
		old := &cosiapi.BucketAccess{}
		new := &cosiapi.BucketAccess{
			Status: cosiapi.BucketAccessStatus{
				DriverName: "something",
			},
		}

		assert.True(t, handoffOccurred(logger, old, new))
	})

}

func TestBucketReadinessChanged(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, cosiapi.AddToScheme(scheme))

	bucketWithReady := func(ready *bool) *cosiapi.Bucket {
		return &cosiapi.Bucket{Status: cosiapi.BucketStatus{ReadyToUse: ready}}
	}

	predicate := BucketReadinessChanged(scheme)

	tests := []struct {
		name     string
		old, new *bool
		want     bool
	}{
		{"nil to nil", nil, nil, false},
		{"nil to false", nil, ptr.To(false), true},
		{"nil to true", nil, ptr.To(true), true},
		{"false to nil", ptr.To(false), nil, true},
		{"false to false", ptr.To(false), ptr.To(false), false},
		{"false to true", ptr.To(false), ptr.To(true), true},
		{"true to nil", ptr.To(true), nil, true},
		{"true to false", ptr.To(true), ptr.To(false), true},
		{"true to true", ptr.To(true), ptr.To(true), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := event.UpdateEvent{ObjectOld: bucketWithReady(tt.old), ObjectNew: bucketWithReady(tt.new)}
			assert.Equal(t, tt.want, predicate.Update(e))
		})
	}

	t.Run("other status fields changing alone is ignored", func(t *testing.T) {
		old := bucketWithReady(ptr.To(true))
		new := old.DeepCopy()
		new.Status.BucketID = "some-bucket-id"
		new.Status.Protocols = []cosiapi.ObjectProtocol{cosiapi.ObjectProtocolS3}
		new.Status.BucketInfo = map[string]string{"endpoint": "https://s3.example.com"}

		assert.False(t, predicate.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: new}))
	})

	t.Run("non-Bucket objects are ignored", func(t *testing.T) {
		claim := &cosiapi.BucketClaim{}
		bucket := bucketWithReady(ptr.To(true))

		assert.False(t, predicate.Update(event.UpdateEvent{ObjectOld: claim, ObjectNew: bucket}))
		assert.False(t, predicate.Update(event.UpdateEvent{ObjectOld: bucket, ObjectNew: claim}))
	})

	t.Run("non-Update events are ignored", func(t *testing.T) {
		bucket := bucketWithReady(ptr.To(true))

		assert.False(t, predicate.Create(event.CreateEvent{Object: bucket}))
		assert.False(t, predicate.Delete(event.DeleteEvent{Object: bucket}))
		assert.False(t, predicate.Generic(event.GenericEvent{Object: bucket}))
	})
}
