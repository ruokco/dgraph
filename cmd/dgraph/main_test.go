/*
 * Copyright (C) 2017 Dgraph Labs, Inc. and Contributors
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

package main

import (
	"bytes"
	"context"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dgraph-io/dgraph/gql"
	"github.com/dgraph-io/dgraph/group"
	"github.com/dgraph-io/dgraph/posting"
	"github.com/dgraph-io/dgraph/query"
	"github.com/dgraph-io/dgraph/schema"
	"github.com/dgraph-io/dgraph/store"
	"github.com/dgraph-io/dgraph/types"
	"github.com/dgraph-io/dgraph/worker"
	"github.com/dgraph-io/dgraph/x"
)

var q0 = `
	{
		user(id:alice) {
			name
		}
	}
`
var m = `
	mutation {
		set {
                        # comment line should be ignored
			<alice> <name> "Alice" .
		}
	}
`

func prepare() (dir1, dir2 string, ps *store.Store, rerr error) {
	var err error
	dir1, err = ioutil.TempDir("", "storetest_")
	if err != nil {
		return "", "", nil, err
	}
	ps, err = store.NewStore(dir1)
	if err != nil {
		return "", "", nil, err
	}

	dir2, err = ioutil.TempDir("", "wal_")
	if err != nil {
		return dir1, "", nil, err
	}

	posting.Init(ps)
	group.ParseGroupConfig("groups.conf")
	schema.Init(ps)
	worker.StartRaftNodes(dir2)

	return dir1, dir2, ps, nil
}

func closeAll(dir1, dir2 string) {
	os.RemoveAll(dir2)
	os.RemoveAll(dir1)
}

func childAttrs(sg *query.SubGraph) []string {
	var out []string
	for _, c := range sg.Children {
		out = append(out, c.Attr)
	}
	return out
}

func processToFastJSON(q string) string {
	res, err := gql.Parse(q)
	if err != nil {
		log.Fatal(err)
	}

	var l query.Latency
	ctx := context.Background()
	sgl, err := query.ProcessQuery(ctx, res, &l)

	if err != nil {
		log.Fatal(err)
	}

	var buf bytes.Buffer
	err = query.ToJson(&l, sgl, &buf, nil)
	if err != nil {
		log.Fatal(err)
	}
	return string(buf.Bytes())
}

func TestQuery(t *testing.T) {
	res, err := gql.Parse(m)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = mutationHandler(ctx, res.Mutation)

	output := processToFastJSON(q0)
	require.JSONEq(t, `{"user":[{"name":"Alice"}]}`, output)
}

var m5 = `
	mutation {
		set {
                        # comment line should be ignored
			<ram> <name> "1"^^<xs:int> .
			<shyam> <name> "abc"^^<xs:int> .
		}
	}
`

var q5 = `
	{
		user(id:<id>) {
			name
		}
	}
`

func TestSchemaValidationError(t *testing.T) {
	res, err := gql.Parse(m5)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = mutationHandler(ctx, res.Mutation)

	require.Error(t, err)
	output := processToFastJSON(strings.Replace(q5, "<id>", "ram", -1))
	require.JSONEq(t, `{}`, output)
}

var m6 = `
	mutation {
		set {
                        # comment line should be ignored
			<ram2> <name2> "1"^^<xs:int> .
			<shyam2> <name2> "1.5"^^<xs:float> .
		}
	}
`

var q6 = `
	{
		user(id:<id>) {
			name2
		}
	}
`

func TestSchemaConversion(t *testing.T) {
	res, err := gql.Parse(m6)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = mutationHandler(ctx, res.Mutation)

	require.NoError(t, err)
	output := processToFastJSON(strings.Replace(q6, "<id>", "shyam2", -1))
	require.JSONEq(t, `{"user":[{"name2":1}]}`, output)

	schema.State().SetType("name2", types.FloatID)
	output = processToFastJSON(strings.Replace(q6, "<id>", "shyam2", -1))
	require.JSONEq(t, `{"user":[{"name2":1.5}]}`, output)
}

var qErr = `
	mutation {
		set {
			<0x0> <name> "Alice" .
		}
	}
`

func TestMutationError(t *testing.T) {
	res, err := gql.Parse(qErr)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = mutationHandler(ctx, res.Mutation)
	require.Error(t, err)

}

var qm = `
	mutation {
		set {
			<0x0a> <pred.rel> _:x .
			_:x <pred.val> "value" .
			_:x <pred.rel> _:y .
			_:y <pred.val> "value2" .
		}
	}
`

func TestAssignUid(t *testing.T) {
	res, err := gql.Parse(qm)
	require.NoError(t, err)

	ctx := context.Background()
	allocIds, err := mutationHandler(ctx, res.Mutation)
	require.NoError(t, err)

	require.EqualValues(t, len(allocIds), 2, "Expected two UIDs to be allocated")
	_, ok := allocIds["x"]
	require.True(t, ok)
	_, ok = allocIds["y"]
	require.True(t, ok)
}

func TestConvertToEdges(t *testing.T) {
	q1 := `<0x01> <type> <0x02> .
	       <0x01> <character> <0x03> .`
	nquads, err := convertToNQuad(context.Background(), q1)
	require.NoError(t, err)

	mr, err := convertToEdges(context.Background(), nquads)
	require.NoError(t, err)

	require.EqualValues(t, len(mr.edges), 2)
}

var q1 = `
{
	al(id: alice) {
		status
		follows {
			status
			follows {
				status
				follows {
					status
				}
			}
		}
	}
}
`

func BenchmarkQuery(b *testing.B) {
	dir1, dir2, _, err := prepare()
	if err != nil {
		b.Error(err)
		return
	}
	defer closeAll(dir1, dir2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		processToFastJSON(q1)
	}
}

func TestMain(m *testing.M) {
	x.SetTestRun()
	x.Init()
	dir1, dir2, _, _ := prepare()
	defer closeAll(dir1, dir2)
	time.Sleep(5 * time.Second) // Wait for ME to become leader.

	// Parse GQL into internal query representation.
	os.Exit(m.Run())
}
