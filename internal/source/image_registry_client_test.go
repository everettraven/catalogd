package source_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/catalogd/api/core/v1alpha1"
	"github.com/operator-framework/catalogd/internal/source"
	"github.com/operator-framework/catalogd/pkg/errors"
)

func TestImageRegistry(t *testing.T) {
	for _, tt := range []struct {
		name string
		// catalog is the Catalog passed to the Unpack function.
		// if the Catalog.Spec.Source.Image.Ref field is empty,
		// one is injected during test runtime to ensure it
		// points to the registry created for the test
		catalog             *v1alpha1.Catalog
		wantErr             bool
		expectedErrType     error
		image               v1.Image
		digestAlreadyExists bool
		oldDigestExists     bool
		// refType is the type of image ref this test
		// is using. Should be one of "tag","digest"
		refType string
	}{
		{
			name: ".spec.source.image is nil",
			catalog: &v1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: v1alpha1.CatalogSpec{
					Source: v1alpha1.CatalogSource{
						Type:  v1alpha1.SourceTypeImage,
						Image: nil,
					},
				},
			},
			wantErr:         true,
			expectedErrType: errors.Unrecoverable{},
			refType:         "tag",
		},
		{
			name: ".spec.source.image.ref is unparsable",
			catalog: &v1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: v1alpha1.CatalogSpec{
					Source: v1alpha1.CatalogSource{
						Type: v1alpha1.SourceTypeImage,
						Image: &v1alpha1.ImageSource{
							Ref: "::)12-as^&8asd789A(::",
						},
					},
				},
			},
			wantErr:         true,
			expectedErrType: errors.Unrecoverable{},
			refType:         "tag",
		},
		{
			name: "image is missing required label",
			catalog: &v1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: v1alpha1.CatalogSpec{
					Source: v1alpha1.CatalogSource{
						Type: v1alpha1.SourceTypeImage,
						Image: &v1alpha1.ImageSource{
							Ref: "",
						},
					},
				},
			},
			wantErr:         true,
			expectedErrType: errors.Unrecoverable{},
			image: func() v1.Image {
				img, err := random.Image(20, 3)
				if err != nil {
					panic(err)
				}
				return img
			}(),
			refType: "tag",
		},
		{
			name: "image doesn't exist",
			catalog: &v1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: v1alpha1.CatalogSpec{
					Source: v1alpha1.CatalogSource{
						Type: v1alpha1.SourceTypeImage,
						Image: &v1alpha1.ImageSource{
							Ref: "",
						},
					},
				},
			},
			wantErr: true,
			refType: "tag",
		},
		{
			name: "tag based image, digest already exists in cache",
			catalog: &v1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: v1alpha1.CatalogSpec{
					Source: v1alpha1.CatalogSource{
						Type: v1alpha1.SourceTypeImage,
						Image: &v1alpha1.ImageSource{
							Ref: "",
						},
					},
				},
			},
			wantErr: false,
			image: func() v1.Image {
				img, err := random.Image(20, 3)
				if err != nil {
					panic(err)
				}
				return img
			}(),
			digestAlreadyExists: true,
			refType:             "tag",
		},
		{
			name: "digest based image, digest already exists in cache",
			catalog: &v1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: v1alpha1.CatalogSpec{
					Source: v1alpha1.CatalogSource{
						Type: v1alpha1.SourceTypeImage,
						Image: &v1alpha1.ImageSource{
							Ref: "",
						},
					},
				},
			},
			wantErr:             false,
			digestAlreadyExists: true,
			refType:             "digest",
			image: func() v1.Image {
				img, err := random.Image(20, 3)
				if err != nil {
					panic(err)
				}
				return img
			}(),
		},
		{
			name: "old ref is cached",
			catalog: &v1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: v1alpha1.CatalogSpec{
					Source: v1alpha1.CatalogSource{
						Type: v1alpha1.SourceTypeImage,
						Image: &v1alpha1.ImageSource{
							Ref: "",
						},
					},
				},
			},
			wantErr:         false,
			oldDigestExists: true,
			refType:         "tag",
			image: func() v1.Image {
				img, err := random.Image(20, 3)
				if err != nil {
					panic(err)
				}
				img, err = mutate.Config(img, v1.Config{
					Labels: map[string]string{
						source.ConfigDirLabel: "/configs",
					},
				})
				if err != nil {
					panic(err)
				}
				return img
			}(),
		},
		{
			name: "tag ref, happy path",
			catalog: &v1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: v1alpha1.CatalogSpec{
					Source: v1alpha1.CatalogSource{
						Type: v1alpha1.SourceTypeImage,
						Image: &v1alpha1.ImageSource{
							Ref: "",
						},
					},
				},
			},
			wantErr: false,
			refType: "tag",
			image: func() v1.Image {
				img, err := random.Image(20, 3)
				if err != nil {
					panic(err)
				}
				img, err = mutate.Config(img, v1.Config{
					Labels: map[string]string{
						source.ConfigDirLabel: "/configs",
					},
				})
				if err != nil {
					panic(err)
				}
				return img
			}(),
		},
		{
			name: "digest ref, happy path",
			catalog: &v1alpha1.Catalog{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: v1alpha1.CatalogSpec{
					Source: v1alpha1.CatalogSource{
						Type: v1alpha1.SourceTypeImage,
						Image: &v1alpha1.ImageSource{
							Ref: "",
						},
					},
				},
			},
			wantErr: false,
			refType: "digest",
			image: func() v1.Image {
				img, err := random.Image(20, 3)
				if err != nil {
					panic(err)
				}
				img, err = mutate.Config(img, v1.Config{
					Labels: map[string]string{
						source.ConfigDirLabel: "/configs",
					},
				})
				if err != nil {
					panic(err)
				}
				return img
			}(),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Create context, temporary cache directory,
			// and image registry source
			ctx := context.Background()
			testCache := t.TempDir()
			imgReg := &source.ImageRegistry{
				BaseCachePath: testCache,
			}

			// Start a new server running an image registry
			srv := httptest.NewServer(registry.New())
			defer srv.Close()

			// parse the server url so we can grab just the host
			url, err := url.Parse(srv.URL)
			require.NoError(t, err)

			// Build the proper image name with {registry}/tt.imgName
			imgName, err := name.ParseReference(fmt.Sprintf("%s/%s", url.Host, "test-image:test"))
			require.NoError(t, err)

			// If an old digest should exist in the cache, create one
			if tt.oldDigestExists {
				err = os.MkdirAll(filepath.Join(testCache, tt.catalog.Name, "olddigest"), os.ModePerm)
				require.NoError(t, err)
			}

			var digest v1.Hash
			// if the test specifies a method that returns a v1.Image,
			// call it and push the image to the registry
			if tt.image != nil {
				digest, err = tt.image.Digest()
				require.NoError(t, err)

				// if the digest should already exist in the cache, create it
				if tt.digestAlreadyExists {
					err = os.MkdirAll(filepath.Join(testCache, tt.catalog.Name, digest.Hex), os.ModePerm)
					require.NoError(t, err)
				}

				err = remote.Write(imgName, tt.image)
				require.NoError(t, err)

				// if the image ref should be a digest ref, make it so
				if tt.refType == "digest" {
					imgName, err = name.ParseReference(fmt.Sprintf("%s/%s", url.Host, "test-image@sha256:"+digest.Hex))
					require.NoError(t, err)
				}
			}

			// Inject the image reference if needed
			if tt.catalog.Spec.Source.Image != nil && tt.catalog.Spec.Source.Image.Ref == "" {
				tt.catalog.Spec.Source.Image.Ref = imgName.Name()
			}

			rs, err := imgReg.Unpack(ctx, tt.catalog)
			if !tt.wantErr {
				assert.NoError(t, err)
				assert.Equal(t, fmt.Sprintf("%s@sha256:%s", imgName.Context().Name(), digest.Hex), rs.ResolvedSource.Image.Ref)
				assert.Equal(t, source.StateUnpacked, rs.State)
				assert.DirExists(t, filepath.Join(testCache, tt.catalog.Name, digest.Hex))
				entries, err := os.ReadDir(filepath.Join(testCache, tt.catalog.Name))
				require.NoError(t, err)
				assert.Len(t, entries, 1)
			} else {
				assert.Error(t, err)
				if tt.expectedErrType != nil {
					assert.ErrorAs(t, err, &tt.expectedErrType)
				}
			}
		})
	}
}
