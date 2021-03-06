package node

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/oasisprotocol/oasis-core/go/common/crypto/hash"
)

func TestSerializationLeafNode(t *testing.T) {
	leafNode := &LeafNode{
		Version: 0xDEADBEEF,
		Key:     []byte("a golden key"),
		Value:   []byte("value"),
	}

	rawLeafNodeFull, err := leafNode.MarshalBinary()
	require.NoError(t, err, "MarshalBinary")
	rawLeafNodeCompact, err := leafNode.CompactMarshalBinary()
	require.NoError(t, err, "CompactMarshalBinary")

	for _, rawLeafNode := range [][]byte{rawLeafNodeFull, rawLeafNodeCompact} {
		var decodedLeafNode LeafNode
		err = decodedLeafNode.UnmarshalBinary(rawLeafNode)
		require.NoError(t, err, "UnmarshalBinary")

		require.True(t, decodedLeafNode.Clean)
		require.Equal(t, leafNode.Version, decodedLeafNode.Version)
		require.Equal(t, leafNode.Key, decodedLeafNode.Key)
		require.Equal(t, leafNode.Value, decodedLeafNode.Value)
	}
}

func TestSerializationInternalNode(t *testing.T) {
	leafNode := &LeafNode{
		Key:   []byte("a golden key"),
		Value: []byte("value"),
	}
	leafNode.UpdateHash()

	leftHash := hash.NewFromBytes([]byte("everyone move to the left"))
	rightHash := hash.NewFromBytes([]byte("everyone move to the right"))
	label := Key("abc")
	labelBitLength := Depth(24)

	intNode := &InternalNode{
		Version:        0xDEADBEEF,
		Label:          label,
		LabelBitLength: labelBitLength,
		LeafNode:       &Pointer{Clean: true, Node: leafNode, Hash: leafNode.Hash},
		Left:           &Pointer{Clean: true, Hash: leftHash},
		Right:          &Pointer{Clean: true, Hash: rightHash},
	}

	rawIntNodeFull, err := intNode.MarshalBinary()
	require.NoError(t, err, "MarshalBinary")
	rawIntNodeCompact, err := intNode.CompactMarshalBinary()
	require.NoError(t, err, "CompactMarshalBinary")

	for idx, rawIntNode := range [][]byte{rawIntNodeFull, rawIntNodeCompact} {
		var decodedIntNode InternalNode
		err = decodedIntNode.UnmarshalBinary(rawIntNode)
		require.NoError(t, err, "UnmarshalBinary")

		require.True(t, decodedIntNode.Clean)
		require.Equal(t, intNode.Version, decodedIntNode.Version)
		require.Equal(t, intNode.Label, decodedIntNode.Label)
		require.Equal(t, intNode.LabelBitLength, decodedIntNode.LabelBitLength)
		require.Equal(t, intNode.LeafNode.Hash, decodedIntNode.LeafNode.Hash)
		require.True(t, decodedIntNode.LeafNode.Clean)
		require.NotNil(t, decodedIntNode.LeafNode.Node)

		// Only check left/right for non-compact encoding.
		if idx == 0 {
			require.Equal(t, intNode.Left.Hash, decodedIntNode.Left.Hash)
			require.Equal(t, intNode.Right.Hash, decodedIntNode.Right.Hash)
			require.True(t, decodedIntNode.Left.Clean)
			require.True(t, decodedIntNode.Right.Clean)
			require.Nil(t, decodedIntNode.Left.Node)
			require.Nil(t, decodedIntNode.Right.Node)
		}
	}
}

func TestHashLeafNode(t *testing.T) {
	leafNode := &LeafNode{
		Version: 0xDEADBEEF,
		Key:     []byte("a golden key"),
		Value:   []byte("value"),
	}

	leafNode.UpdateHash()

	require.Equal(t, leafNode.Hash.String(), "1bf37ec60c5494775e7029ec2a888c42d14f9710852c86ffe0afab8e3c43b782")
}

func TestHashInternalNode(t *testing.T) {
	leafNodeHash := hash.NewFromBytes([]byte("everyone stop here"))
	leftHash := hash.NewFromBytes([]byte("everyone move to the left"))
	rightHash := hash.NewFromBytes([]byte("everyone move to the right"))

	intNode := &InternalNode{
		Version:        0xDEADBEEF,
		Label:          Key("abc"),
		LabelBitLength: 23,
		LeafNode:       &Pointer{Clean: true, Hash: leafNodeHash},
		Left:           &Pointer{Clean: true, Hash: leftHash},
		Right:          &Pointer{Clean: true, Hash: rightHash},
	}

	intNode.UpdateHash()

	require.Equal(t, "e760353e9796f41b3bb2cfa2cf45f7e00ca687b6b84dc658e0ecadc906d5d21e", intNode.Hash.String())
}

func TestExtractLeafNode(t *testing.T) {
	leafNode := &LeafNode{
		Clean:   true,
		Version: 0xDEADBEEF,
		Key:     []byte("a golden key"),
		Value:   []byte("value"),
	}

	exLeafNode := leafNode.Extract().(*LeafNode)

	require.False(t, leafNode == exLeafNode, "extracted node must have a different address")
	require.False(t, &leafNode.Value == &exLeafNode.Value, "extracted value must have a different address")
	require.Equal(t, true, exLeafNode.Clean, "extracted leaf must be clean")
	require.Equal(t, leafNode.Version, exLeafNode.Version, "extracted leaf must have the same version")
	require.EqualValues(t, leafNode.Key, exLeafNode.Key, "extracted leaf must have the same key")
	require.EqualValues(t, leafNode.Value, exLeafNode.Value, "extracted leaf's value must have the same value")
}

func TestExtractInternalNode(t *testing.T) {
	leftHash := hash.NewFromBytes([]byte("everyone move to the left"))
	rightHash := hash.NewFromBytes([]byte("everyone move to the right"))

	intNode := &InternalNode{
		Clean:   true,
		Version: 0xDEADBEEF,
		Left:    &Pointer{Clean: true, Hash: leftHash},
		Right:   &Pointer{Clean: true, Hash: rightHash},
	}

	exIntNode := intNode.Extract().(*InternalNode)

	require.False(t, intNode == exIntNode, "extracted node must have a different address")
	require.False(t, intNode.Left == exIntNode.Left, "extracted left pointer must have a different address")
	require.False(t, intNode.Right == exIntNode.Right, "extracted right pointer must have a different address")
	require.Equal(t, true, exIntNode.Clean, "extracted internal node must be clean")
	require.Equal(t, intNode.Version, exIntNode.Version, "extracted internal node must have the same version")
	require.Equal(t, leftHash, exIntNode.Left.Hash, "extracted left pointer must have the same hash")
	require.Equal(t, true, exIntNode.Left.Clean, "extracted left pointer must be clean")
	require.Equal(t, rightHash, exIntNode.Right.Hash, "extracted right pointer must have the same hash")
	require.Equal(t, true, exIntNode.Right.Clean, "extracted right pointer must be clean")
}
