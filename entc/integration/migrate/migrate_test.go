// Copyright 2019-present Facebook Inc. All rights reserved.
// This source code is licensed under the Apache 2.0 license found
// in the LICENSE file in the root directory of this source tree.

package migrate

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/facebook/ent/dialect"
	"github.com/facebook/ent/dialect/sql"
	"github.com/facebook/ent/entc/integration/migrate/entv1"
	migratev1 "github.com/facebook/ent/entc/integration/migrate/entv1/migrate"
	userv1 "github.com/facebook/ent/entc/integration/migrate/entv1/user"
	"github.com/facebook/ent/entc/integration/migrate/entv2"
	"github.com/facebook/ent/entc/integration/migrate/entv2/conversion"
	migratev2 "github.com/facebook/ent/entc/integration/migrate/entv2/migrate"
	"github.com/facebook/ent/entc/integration/migrate/entv2/user"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestMySQL(t *testing.T) {
	for version, port := range map[string]int{"56": 3306, "57": 3307, "8": 3308} {
		t.Run(version, func(t *testing.T) {
			root, err := sql.Open("mysql", fmt.Sprintf("root:pass@tcp(localhost:%d)/", port))
			require.NoError(t, err)
			defer root.Close()
			ctx := context.Background()
			err = root.Exec(ctx, "CREATE DATABASE IF NOT EXISTS migrate", []interface{}{}, new(sql.Result))
			require.NoError(t, err, "creating database")
			defer root.Exec(ctx, "DROP DATABASE IF EXISTS migrate", []interface{}{}, new(sql.Result))

			drv, err := sql.Open("mysql", fmt.Sprintf("root:pass@tcp(localhost:%d)/migrate?parseTime=True", port))
			require.NoError(t, err, "connecting to migrate database")

			clientv1 := entv1.NewClient(entv1.Driver(drv))
			clientv2 := entv2.NewClient(entv2.Driver(drv))
			V1ToV2(t, drv.Dialect(), clientv1, clientv2)
		})
	}
}

func TestPostgres(t *testing.T) {
	for version, port := range map[string]int{"10": 5430, "11": 5431, "12": 5433, "13": 5434} {
		t.Run(version, func(t *testing.T) {
			dsn := fmt.Sprintf("host=localhost port=%d user=postgres password=pass sslmode=disable", port)
			root, err := sql.Open(dialect.Postgres, dsn)
			require.NoError(t, err)
			defer root.Close()
			ctx := context.Background()
			err = root.Exec(ctx, "DROP DATABASE IF EXISTS migrate", []interface{}{}, new(sql.Result))
			require.NoError(t, err)
			err = root.Exec(ctx, "CREATE DATABASE migrate", []interface{}{}, new(sql.Result))
			require.NoError(t, err, "creating database")
			defer root.Exec(ctx, "DROP DATABASE migrate", []interface{}{}, new(sql.Result))

			drv, err := sql.Open(dialect.Postgres, dsn+" dbname=migrate")
			require.NoError(t, err, "connecting to migrate database")
			defer drv.Close()

			err = drv.Exec(ctx, "CREATE TYPE customtype as range (subtype = time)", []interface{}{}, new(sql.Result))
			require.NoError(t, err, "creating custom type")

			clientv1 := entv1.NewClient(entv1.Driver(drv))
			clientv2 := entv2.NewClient(entv2.Driver(drv))
			V1ToV2(t, drv.Dialect(), clientv1, clientv2)
		})
	}
}

func TestSQLite(t *testing.T) {
	drv, err := sql.Open("sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	require.NoError(t, err)
	defer drv.Close()

	ctx := context.Background()
	client := entv2.NewClient(entv2.Driver(drv))
	require.NoError(t, client.Schema.Create(ctx, migratev2.WithGlobalUniqueID(true)), migratev2.WithDropIndex(true))

	SanityV2(t, drv.Dialect(), client)
	idRange(t, client.Car.Create().SaveX(ctx).ID, 0, 1<<32)
	idRange(t, client.Conversion.Create().SaveX(ctx).ID, 1<<32-1, 2<<32)
	idRange(t, client.CustomType.Create().SaveX(ctx).ID, 2<<32-1, 3<<32)
	idRange(t, client.Group.Create().SaveX(ctx).ID, 3<<32-1, 4<<32)
	idRange(t, client.Media.Create().SaveX(ctx).ID, 4<<32-1, 5<<32)
	idRange(t, client.Pet.Create().SaveX(ctx).ID, 5<<32-1, 6<<32)
	idRange(t, client.User.Create().SetAge(1).SetName("x").SetNickname("x'").SetPhone("y").SaveX(ctx).ID, 6<<32-1, 7<<32)

	// Override the default behavior of LIKE in SQLite.
	// https://www.sqlite.org/pragma.html#pragma_case_sensitive_like
	_, err = drv.ExecContext(ctx, "PRAGMA case_sensitive_like=1")
	require.NoError(t, err)
	EqualFold(t, client)
	ContainsFold(t, client)
}

func V1ToV2(t *testing.T, dialect string, clientv1 *entv1.Client, clientv2 *entv2.Client) {
	ctx := context.Background()

	// Run migration and execute queries on v1.
	require.NoError(t, clientv1.Schema.Create(ctx, migratev1.WithGlobalUniqueID(true)))
	SanityV1(t, dialect, clientv1)

	// Run migration and execute queries on v2.
	require.NoError(t, clientv2.Schema.Create(ctx, migratev2.WithGlobalUniqueID(true), migratev2.WithDropIndex(true), migratev2.WithDropColumn(true)))
	require.NoError(t, clientv2.Schema.Create(ctx, migratev2.WithGlobalUniqueID(true)), "should not create additional resources on multiple runs")
	SanityV2(t, dialect, clientv2)

	idRange(t, clientv2.Car.Create().SaveX(ctx).ID, 0, 1<<32)
	idRange(t, clientv2.Conversion.Create().SaveX(ctx).ID, 1<<32-1, 2<<32)
	// Since "users" created in the migration of v1, it will occupy the range of 1<<32-1 ... 2<<32-1,
	// even though they are ordered differently in the migration of v2 (groups, pets, users).
	idRange(t, clientv2.User.Create().SetAge(1).SetName("foo").SetNickname("nick_foo").SetPhone("phone").SaveX(ctx).ID, 3<<32-1, 4<<32)
	idRange(t, clientv2.Group.Create().SaveX(ctx).ID, 4<<32-1, 5<<32)
	idRange(t, clientv2.Media.Create().SaveX(ctx).ID, 5<<32-1, 6<<32)
	idRange(t, clientv2.Pet.Create().SaveX(ctx).ID, 6<<32-1, 7<<32)

	// SQL specific predicates.
	EqualFold(t, clientv2)
	ContainsFold(t, clientv2)

	// "renamed" field was renamed to "new_name".
	exist := clientv2.User.Query().Where(user.NewName("renamed")).ExistX(ctx)
	require.True(t, exist, "expect renamed column to have previous values")
}

func SanityV1(t *testing.T, dbdialect string, client *entv1.Client) {
	ctx := context.Background()
	u := client.User.Create().SetAge(1).SetName("foo").SetNickname("nick_foo").SetRenamed("renamed").SaveX(ctx)
	require.EqualValues(t, 1, u.Age)
	require.Equal(t, "foo", u.Name)

	_, err := client.User.Create().SetAge(2).SetName("foobarbazqux").Save(ctx)
	require.Error(t, err, "name is limited to 10 chars")

	// Unique index on (name, address).
	client.User.Create().SetAge(3).SetName("foo").SetNickname("nick_foo_2").SetAddress("tlv").SetState(userv1.StateLoggedIn).SaveX(ctx)
	_, err = client.User.Create().SetAge(4).SetName("foo").SetAddress("tlv").Save(ctx)
	require.Error(t, err)

	// Blob type limited to 255.
	u = u.Update().SetBlob([]byte("hello")).SaveX(ctx)
	require.Equal(t, "hello", string(u.Blob))
	_, err = u.Update().SetBlob(make([]byte, 256)).Save(ctx)
	require.True(t, strings.Contains(t.Name(), "Postgres") || err != nil, "blob should be limited on SQLite and MySQL")

	// Invalid enum value.
	_, err = client.User.Create().SetAge(1).SetName("bar").SetNickname("nick_bar").SetState("unknown").Save(ctx)
	require.Error(t, err)

	// Conversions
	client.Conversion.Create().
		SetName("zero").
		SetInt8ToString(0).
		SetUint8ToString(0).
		SetInt16ToString(0).
		SetUint16ToString(0).
		SetInt32ToString(0).
		SetUint32ToString(0).
		SetInt64ToString(0).
		SetUint64ToString(0).
		SaveX(ctx)

	client.Conversion.Create().
		SetName("min").
		SetInt8ToString(math.MinInt8).
		SetUint8ToString(0).
		SetInt16ToString(math.MinInt16).
		SetUint16ToString(0).
		SetInt32ToString(math.MinInt32).
		SetUint32ToString(0).
		SetInt64ToString(math.MinInt64).
		SetUint64ToString(0).
		SaveX(ctx)

	creator := client.Conversion.Create().
		SetName("max").
		SetInt8ToString(math.MaxInt8).
		SetUint8ToString(math.MaxUint8).
		SetInt16ToString(math.MaxInt16).
		SetUint16ToString(math.MaxUint16).
		SetInt32ToString(math.MaxInt32).
		SetUint32ToString(math.MaxUint32).
		SetInt64ToString(math.MaxInt64).
		SetUint64ToString(math.MaxUint64)
	if dbdialect == dialect.Postgres {
		// Postgres does not support unsigned types.
		creator.SetInt8ToString(math.MaxInt8).
			SetUint8ToString(math.MaxInt8).
			SetUint16ToString(math.MaxInt16).
			SetUint32ToString(math.MaxInt32).
			SetUint32ToString(math.MaxInt32).
			SetUint64ToString(math.MaxInt64)
	}
	creator.SaveX(ctx)
}

func SanityV2(t *testing.T, dbdialect string, client *entv2.Client) {
	ctx := context.Background()
	u := client.User.Create().SetAge(1).SetName("bar").SetNickname("nick_bar").SetPhone("100").SetBuffer([]byte("{}")).SetState(user.StateLoggedOut).SaveX(ctx)
	require.Equal(t, 1, u.Age)
	require.Equal(t, "bar", u.Name)
	require.Equal(t, []byte("{}"), u.Buffer)
	u = u.Update().SetBuffer([]byte("[]")).SaveX(ctx)
	require.Equal(t, []byte("[]"), u.Buffer)
	require.Equal(t, user.StateLoggedOut, u.State)

	_, err := u.Update().SetState(user.State("boring")).Save(ctx)
	require.Error(t, err, "invalid enum value")
	u = u.Update().SetState(user.StateOnline).SaveX(ctx)
	require.Equal(t, user.StateOnline, u.State)

	_, err = client.User.Create().SetAge(1).SetName("foobarbazqux").SetNickname("nick_bar").SetPhone("200").Save(ctx)
	require.NoError(t, err, "name is not limited to 10 chars and nickname is not unique")

	// New unique index was added to (age, phone).
	_, err = client.User.Create().SetAge(1).SetName("foo").SetPhone("200").SetNickname("nick_bar").Save(ctx)
	require.Error(t, err)
	require.True(t, entv2.IsConstraintError(err))

	// Ensure all rows in the database have the same default for the `title` column.
	require.Equal(
		t,
		client.User.Query().CountX(ctx),
		client.User.Query().Where(user.Title(user.DefaultTitle)).CountX(ctx),
	)

	// Blob type was extended.
	u, err = u.Update().SetBlob(make([]byte, 256)).SetState(user.StateLoggedOut).Save(ctx)
	require.NoError(t, err, "data type blob was extended in v2")
	require.Equal(t, make([]byte, 256), u.Blob)

	if dbdialect != dialect.SQLite {
		// Conversions
		zero := client.Conversion.Query().Where(conversion.Name("zero")).OnlyX(ctx)
		require.Equal(t, strconv.Itoa(0), zero.Int8ToString)
		require.Equal(t, strconv.Itoa(0), zero.Uint8ToString)
		require.Equal(t, strconv.Itoa(0), zero.Int16ToString)
		require.Equal(t, strconv.Itoa(0), zero.Uint16ToString)
		require.Equal(t, strconv.Itoa(0), zero.Int32ToString)
		require.Equal(t, strconv.Itoa(0), zero.Uint32ToString)
		require.Equal(t, strconv.Itoa(0), zero.Int64ToString)
		require.Equal(t, strconv.Itoa(0), zero.Uint64ToString)

		min := client.Conversion.Query().Where(conversion.Name("min")).OnlyX(ctx)
		require.Equal(t, strconv.Itoa(math.MinInt8), min.Int8ToString)
		require.Equal(t, strconv.Itoa(0), min.Uint8ToString)
		require.Equal(t, strconv.Itoa(math.MinInt16), min.Int16ToString)
		require.Equal(t, strconv.Itoa(0), min.Uint16ToString)
		require.Equal(t, strconv.Itoa(math.MinInt32), min.Int32ToString)
		require.Equal(t, strconv.Itoa(0), min.Uint32ToString)
		require.Equal(t, strconv.Itoa(math.MinInt64), min.Int64ToString)
		require.Equal(t, strconv.Itoa(0), min.Uint64ToString)

		max := client.Conversion.Query().Where(conversion.Name("max")).OnlyX(ctx)
		require.Equal(t, strconv.Itoa(math.MaxInt8), max.Int8ToString)
		require.Equal(t, strconv.Itoa(math.MaxInt16), max.Int16ToString)
		require.Equal(t, strconv.Itoa(math.MaxInt32), max.Int32ToString)
		require.Equal(t, strconv.Itoa(math.MaxInt64), max.Int64ToString)

		if dbdialect == dialect.Postgres {
			require.Equal(t, strconv.Itoa(math.MaxInt8), max.Uint8ToString)
			require.Equal(t, strconv.Itoa(math.MaxInt16), max.Uint16ToString)
			require.Equal(t, strconv.Itoa(math.MaxInt32), max.Uint32ToString)
			require.Equal(t, strconv.Itoa(math.MaxInt64), max.Uint64ToString)
		} else {
			require.Equal(t, strconv.Itoa(math.MaxUint8), max.Uint8ToString)
			require.Equal(t, strconv.Itoa(math.MaxUint16), max.Uint16ToString)
			require.Equal(t, strconv.Itoa(math.MaxUint32), max.Uint32ToString)
			require.Equal(t, strconv.FormatUint(math.MaxUint64, 10), max.Uint64ToString)
		}
	}
}

func EqualFold(t *testing.T, client *entv2.Client) {
	ctx := context.Background()
	t.Log("testing equal-fold on sql specific dialects")
	client.User.Create().SetAge(37).SetName("Alex").SetNickname("alexsn").SetPhone("123456789").SaveX(ctx)
	require.False(t, client.User.Query().Where(user.NameEQ("alex")).ExistX(ctx))
	require.True(t, client.User.Query().Where(user.NameEqualFold("alex")).ExistX(ctx))
}

func ContainsFold(t *testing.T, client *entv2.Client) {
	ctx := context.Background()
	t.Log("testing contains-fold on sql specific dialects")
	client.User.Create().SetAge(30).SetName("Mashraki").SetNickname("a8m").SetPhone("102030").SaveX(ctx)
	require.Zero(t, client.User.Query().Where(user.NameContains("mash")).CountX(ctx))
	require.Equal(t, 1, client.User.Query().Where(user.NameContainsFold("mash")).CountX(ctx))
	require.Equal(t, 1, client.User.Query().Where(user.NameContainsFold("Raki")).CountX(ctx))
}

func idRange(t *testing.T, id, l, h int) {
	require.Truef(t, id > l && id < h, "id %s should be between %d to %d", id, l, h)
}
