package query_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/influxdata/flux"
	platform "github.com/influxdata/influxdb"
	"github.com/influxdata/influxdb/inmem"
	"github.com/influxdata/influxdb/mock"
	"github.com/influxdata/influxdb/query"
	_ "github.com/influxdata/influxdb/query/builtin"
)

func newBucketServiceWithOneBucket(bucket platform.Bucket) platform.BucketService {
	bs := mock.NewBucketService()
	bs.FindBucketFn = func(ctx context.Context, bucketFilter platform.BucketFilter) (*platform.Bucket, error) {
		if *bucketFilter.Name == bucket.Name {
			return &bucket, nil
		}

		return nil, errors.New("unknown bucket")
	}

	return bs
}

func TestPreAuthorizer_PreAuthorize(t *testing.T) {
	ctx := context.Background()
	// fresh pre-authorizer
	auth := &platform.Authorization{Status: platform.Active}
	emptyBucketService := mock.NewBucketService()
	preAuthorizer := query.NewPreAuthorizer(emptyBucketService)

	// Try to pre-authorize invalid bucketID
	q := `from(bucketID:"invalid") |> range(start:-2h) |> yield()`
	ast, err := flux.Parse(q)
	if err != nil {
		t.Fatalf("Error compiling query: %v", err)
	}
	err = preAuthorizer.PreAuthorize(ctx, ast, auth, nil)
	if diagnostic := cmp.Diff("bucket service returned nil bucket", err.Error()); diagnostic != "" {
		t.Errorf("Authorize message mismatch: -want/+got:\n%v", diagnostic)
	}

	// Try to pre-authorize a valid from with bucket service with no buckets
	// and no authorization
	q = `from(bucket:"my_bucket") |> range(start:-2h) |> yield()`
	ast, err = flux.Parse(q)
	if err != nil {
		t.Fatalf("Error compiling query: %v", err)
	}
	err = preAuthorizer.PreAuthorize(ctx, ast, auth, nil)
	if diagnostic := cmp.Diff("bucket service returned nil bucket", err.Error()); diagnostic != "" {
		t.Errorf("Authorize message mismatch: -want/+got:\n%v", diagnostic)
	}

	// Try to authorize with a bucket service that knows about one bucket
	// (still no authorization)
	bucketID, err := platform.IDFromString("deadbeefdeadbeef")
	if err != nil {
		t.Fatal(err)
	}
	orgID := platform.ID(1)
	bucketService := newBucketServiceWithOneBucket(platform.Bucket{
		Name:  "my_bucket",
		ID:    *bucketID,
		OrgID: orgID,
	})

	preAuthorizer = query.NewPreAuthorizer(bucketService)
	err = preAuthorizer.PreAuthorize(ctx, ast, auth, &orgID)
	if diagnostic := cmp.Diff(`no read permission for bucket: "my_bucket"`, err.Error()); diagnostic != "" {
		t.Errorf("Authorize message mismatch: -want/+got:\n%v", diagnostic)
	}

	p, err := platform.NewPermissionAtID(*bucketID, platform.ReadAction, platform.BucketsResourceType, orgID)
	if err != nil {
		t.Fatalf("Error creating read bucket permission query: %v", err)
	}
	// Try to authorize with read permission on bucket
	auth = &platform.Authorization{
		Status:      platform.Active,
		Permissions: []platform.Permission{*p},
	}

	err = preAuthorizer.PreAuthorize(ctx, ast, auth, &orgID)
	if err != nil {
		t.Errorf("Expected successful authorization, but got error: \"%v\"", err.Error())
	}
}

func TestPreAuthorizer_RequiredPermissions(t *testing.T) {
	t.Skip("Re-enable when pre-authorizer works again")
	ctx := context.Background()

	i := inmem.NewService()

	o := platform.Organization{Name: "o"}
	if err := i.CreateOrganization(ctx, &o); err != nil {
		t.Fatal(err)
	}
	bFrom := platform.Bucket{Name: "b-from", OrgID: o.ID}
	if err := i.CreateBucket(ctx, &bFrom); err != nil {
		t.Fatal(err)
	}
	bTo := platform.Bucket{Name: "b-to", OrgID: o.ID}
	if err := i.CreateBucket(ctx, &bTo); err != nil {
		t.Fatal(err)
	}

	const script = `from(bucket:"b-from") |> range(start:-1m) |> to(bucket:"b-to", org:"o")`
	ast, err := flux.Parse(script)
	if err != nil {
		t.Fatal(err)
	}

	preAuthorizer := query.NewPreAuthorizer(i)
	perms, err := preAuthorizer.RequiredPermissions(ctx, ast, &o.ID)
	if err != nil {
		t.Fatal(err)
	}

	pRead, err := platform.NewPermissionAtID(bFrom.ID, platform.ReadAction, platform.BucketsResourceType, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	pWrite, err := platform.NewPermissionAtID(bTo.ID, platform.WriteAction, platform.BucketsResourceType, o.ID)
	if err != nil {
		t.Fatal(err)
	}

	exp := []platform.Permission{*pRead, *pWrite}
	if diff := cmp.Diff(exp, perms); diff != "" {
		t.Fatalf("unexpected permissions: %s", diff)
	}
}
