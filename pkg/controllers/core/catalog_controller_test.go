package core

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/google/go-cmp/cmp/cmpopts"

	catalogdv1alpha1 "github.com/operator-framework/catalogd/api/core/v1alpha1"
	"github.com/operator-framework/catalogd/internal/source"
	"github.com/operator-framework/catalogd/pkg/storage"
)

var _ source.Unpacker = &MockSource{}

// MockSource is a utility for mocking out an Unpacker source
type MockSource struct {
	// result is the result that should be returned when MockSource.Unpack is called
	result *source.Result

	// shouldError determines whether or not the MockSource should return an error when MockSource.Unpack is called
	shouldError bool
}

func (ms *MockSource) Unpack(_ context.Context, _ *catalogdv1alpha1.Catalog) (*source.Result, error) {
	if ms.shouldError {
		return nil, errors.New("mocksource error")
	}

	return ms.result, nil
}

func (ms *MockSource) Cleanup(_ context.Context, _ *catalogdv1alpha1.Catalog) error {
	if ms.shouldError {
		return errors.New("mocksource error")
	}

	return nil
}

var _ storage.Instance = &MockStore{}

type MockStore struct {
	shouldError bool
}

func (m MockStore) Store(_ string, _ fs.FS) error {
	if m.shouldError {
		return errors.New("mockstore store error")
	}
	return nil
}

func (m MockStore) Delete(_ string) error {
	if m.shouldError {
		return errors.New("mockstore delete error")
	}
	return nil
}

func (m MockStore) ContentURL(_ string) string {
	return "URL"
}

func (m MockStore) StorageServerHandler() http.Handler {
	panic("not needed")
}

func TestCatalogdControllerReconcile(t *testing.T) {
	for _, tt := range []struct {
		name            string
		catalog         *catalogdv1alpha1.Catalog
		shouldErr       bool
		expectedCatalog *catalogdv1alpha1.Catalog
		source          source.Unpacker
		store           storage.Instance
	}{
		{
			name:   "invalid source type, returns error",
			source: &MockSource{},
			store:  &MockStore{},
			catalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "catalog",
					Finalizers: []string{fbcDeletionFinalizer},
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "invalid",
					},
				},
			},
			shouldErr: true,
			expectedCatalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "catalog",
					Finalizers: []string{fbcDeletionFinalizer},
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "invalid",
					},
				},
				Status: catalogdv1alpha1.CatalogStatus{
					Phase: catalogdv1alpha1.PhaseFailing,
					Conditions: []metav1.Condition{
						{
							Type:   catalogdv1alpha1.TypeUnpacked,
							Status: metav1.ConditionFalse,
							Reason: catalogdv1alpha1.ReasonUnpackFailed,
						},
					},
				},
			},
		},
		{
			name: "valid source type, unpack state == Pending, unpack state is reflected in status",
			source: &MockSource{
				result: &source.Result{State: source.StatePending},
			},
			store: &MockStore{},
			catalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "catalog",
					Finalizers: []string{fbcDeletionFinalizer},
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "image",
						Image: &catalogdv1alpha1.ImageSource{
							Ref: "someimage:latest",
						},
					},
				},
			},
			expectedCatalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "catalog",
					Finalizers: []string{fbcDeletionFinalizer},
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "image",
						Image: &catalogdv1alpha1.ImageSource{
							Ref: "someimage:latest",
						},
					},
				},
				Status: catalogdv1alpha1.CatalogStatus{
					Phase: catalogdv1alpha1.PhasePending,
					Conditions: []metav1.Condition{
						{
							Type:   catalogdv1alpha1.TypeUnpacked,
							Status: metav1.ConditionFalse,
							Reason: catalogdv1alpha1.ReasonUnpackPending,
						},
					},
				},
			},
		},
		{
			name: "valid source type, unpack state == Unpacking, unpack state is reflected in status",
			source: &MockSource{
				result: &source.Result{State: source.StateUnpacking},
			},
			store: &MockStore{},
			catalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "catalog",
					Finalizers: []string{fbcDeletionFinalizer},
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "image",
						Image: &catalogdv1alpha1.ImageSource{
							Ref: "someimage:latest",
						},
					},
				},
			},
			expectedCatalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "catalog",
					Finalizers: []string{fbcDeletionFinalizer},
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "image",
						Image: &catalogdv1alpha1.ImageSource{
							Ref: "someimage:latest",
						},
					},
				},
				Status: catalogdv1alpha1.CatalogStatus{
					Phase: catalogdv1alpha1.PhaseUnpacking,
					Conditions: []metav1.Condition{
						{
							Type:   catalogdv1alpha1.TypeUnpacked,
							Status: metav1.ConditionFalse,
							Reason: catalogdv1alpha1.ReasonUnpacking,
						},
					},
				},
			},
		},
		{
			name:      "valid source type, unpack state is unknown, unpack state is reflected in status and error is returned",
			shouldErr: true,
			source: &MockSource{
				result: &source.Result{State: "unknown"},
			},
			store: &MockStore{},
			catalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "catalog",
					Finalizers: []string{fbcDeletionFinalizer},
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "image",
						Image: &catalogdv1alpha1.ImageSource{
							Ref: "someimage:latest",
						},
					},
				},
			},
			expectedCatalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "catalog",
					Finalizers: []string{fbcDeletionFinalizer},
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "image",
						Image: &catalogdv1alpha1.ImageSource{
							Ref: "someimage:latest",
						},
					},
				},
				Status: catalogdv1alpha1.CatalogStatus{
					Phase: catalogdv1alpha1.PhaseFailing,
					Conditions: []metav1.Condition{
						{
							Type:   catalogdv1alpha1.TypeUnpacked,
							Status: metav1.ConditionFalse,
							Reason: catalogdv1alpha1.ReasonUnpackFailed,
						},
					},
				},
			},
		},
		{
			name:      "valid source type, unpack returns error, status updated to reflect error state and error is returned",
			shouldErr: true,
			source: &MockSource{
				shouldError: true,
			},
			store: &MockStore{},
			catalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "catalog",
					Finalizers: []string{fbcDeletionFinalizer},
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "image",
						Image: &catalogdv1alpha1.ImageSource{
							Ref: "someimage:latest",
						},
					},
				},
			},
			expectedCatalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "catalog",
					Finalizers: []string{fbcDeletionFinalizer},
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "image",
						Image: &catalogdv1alpha1.ImageSource{
							Ref: "someimage:latest",
						},
					},
				},
				Status: catalogdv1alpha1.CatalogStatus{
					Phase: catalogdv1alpha1.PhaseFailing,
					Conditions: []metav1.Condition{
						{
							Type:   catalogdv1alpha1.TypeUnpacked,
							Status: metav1.ConditionFalse,
							Reason: catalogdv1alpha1.ReasonUnpackFailed,
						},
					},
				},
			},
		},
		{
			name: "valid source type, unpack state == Unpacked, unpack state is reflected in status",
			source: &MockSource{
				result: &source.Result{
					State: source.StateUnpacked,
					FS:    &fstest.MapFS{},
				},
			},
			store: &MockStore{},
			catalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "catalog",
					Finalizers: []string{fbcDeletionFinalizer},
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "image",
						Image: &catalogdv1alpha1.ImageSource{
							Ref: "someimage:latest",
						},
					},
				},
			},
			expectedCatalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "catalog",
					Finalizers: []string{fbcDeletionFinalizer},
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "image",
						Image: &catalogdv1alpha1.ImageSource{
							Ref: "someimage:latest",
						},
					},
				},
				Status: catalogdv1alpha1.CatalogStatus{
					Phase:      catalogdv1alpha1.PhaseUnpacked,
					ContentURL: "URL",
					Conditions: []metav1.Condition{
						{
							Type:   catalogdv1alpha1.TypeUnpacked,
							Status: metav1.ConditionTrue,
							Reason: catalogdv1alpha1.ReasonUnpackSuccessful,
						},
					},
				},
			},
		},
		{
			name:      "valid source type, unpack state == Unpacked, storage fails, failure reflected in status and error returned",
			shouldErr: true,
			source: &MockSource{
				result: &source.Result{
					State: source.StateUnpacked,
					FS:    &fstest.MapFS{},
				},
			},
			store: &MockStore{
				shouldError: true,
			},
			catalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "catalog",
					Finalizers: []string{fbcDeletionFinalizer},
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "image",
						Image: &catalogdv1alpha1.ImageSource{
							Ref: "someimage:latest",
						},
					},
				},
			},
			expectedCatalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "catalog",
					Finalizers: []string{fbcDeletionFinalizer},
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "image",
						Image: &catalogdv1alpha1.ImageSource{
							Ref: "someimage:latest",
						},
					},
				},
				Status: catalogdv1alpha1.CatalogStatus{
					Phase: catalogdv1alpha1.PhaseFailing,
					Conditions: []metav1.Condition{
						{
							Type:   catalogdv1alpha1.TypeUnpacked,
							Status: metav1.ConditionFalse,
							Reason: catalogdv1alpha1.ReasonStorageFailed,
						},
					},
				},
			},
		},
		{
			name:   "storage finalizer not set, storage finalizer gets set",
			source: &MockSource{},
			store:  &MockStore{},
			catalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name: "catalog",
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "image",
						Image: &catalogdv1alpha1.ImageSource{
							Ref: "someimage:latest",
						},
					},
				},
			},
			expectedCatalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "catalog",
					Finalizers: []string{fbcDeletionFinalizer},
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "image",
						Image: &catalogdv1alpha1.ImageSource{
							Ref: "someimage:latest",
						},
					},
				},
			},
		},
		{
			name:   "storage finalizer set, catalog deletion timestamp is not zero (or nil), finalizer removed",
			source: &MockSource{},
			store:  &MockStore{},
			catalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "catalog",
					Finalizers:        []string{fbcDeletionFinalizer},
					DeletionTimestamp: &metav1.Time{Time: time.Date(2023, time.October, 10, 4, 19, 0, 0, time.UTC)},
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "image",
						Image: &catalogdv1alpha1.ImageSource{
							Ref: "someimage:latest",
						},
					},
				},
			},
			expectedCatalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "catalog",
					Finalizers:        []string{},
					DeletionTimestamp: &metav1.Time{Time: time.Date(2023, time.October, 10, 4, 19, 0, 0, time.UTC)},
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "image",
						Image: &catalogdv1alpha1.ImageSource{
							Ref: "someimage:latest",
						},
					},
				},
			},
		},
		{
			name:      "storage finalizer set, catalog deletion timestamp is not zero (or nil), storage delete failed, error returned and finalizer not removed",
			shouldErr: true,
			source:    &MockSource{},
			store: &MockStore{
				shouldError: true,
			},
			catalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "catalog",
					Finalizers:        []string{fbcDeletionFinalizer},
					DeletionTimestamp: &metav1.Time{Time: time.Date(2023, time.October, 10, 4, 19, 0, 0, time.UTC)},
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "image",
						Image: &catalogdv1alpha1.ImageSource{
							Ref: "someimage:latest",
						},
					},
				},
			},
			expectedCatalog: &catalogdv1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "catalog",
					Finalizers:        []string{fbcDeletionFinalizer},
					DeletionTimestamp: &metav1.Time{Time: time.Date(2023, time.October, 10, 4, 19, 0, 0, time.UTC)},
				},
				Spec: catalogdv1alpha1.CatalogSpec{
					Source: catalogdv1alpha1.CatalogSource{
						Type: "image",
						Image: &catalogdv1alpha1.ImageSource{
							Ref: "someimage:latest",
						},
					},
				},
				Status: catalogdv1alpha1.CatalogStatus{
					Conditions: []metav1.Condition{
						{
							Type:   catalogdv1alpha1.TypeDelete,
							Status: metav1.ConditionFalse,
							Reason: catalogdv1alpha1.ReasonStorageDeleteFailed,
						},
					},
				},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &CatalogReconciler{
				Client: nil,
				Unpacker: source.NewUnpacker(
					map[catalogdv1alpha1.SourceType]source.Unpacker{
						catalogdv1alpha1.SourceTypeImage: tt.source,
					},
				),
				Storage: tt.store,
			}
			ctx := context.Background()
			res, err := reconciler.reconcile(ctx, tt.catalog)
			assert.Equal(t, ctrl.Result{}, res)

			if !tt.shouldErr {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}

			diff := cmp.Diff(tt.expectedCatalog, tt.catalog, cmpopts.IgnoreFields(metav1.Condition{}, "Message", "LastTransitionTime"))
			assert.Empty(t, diff, "comparing the expected Catalog")
		})
	}
}
