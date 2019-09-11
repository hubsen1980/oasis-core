use std::{cell::RefCell, mem::size_of, rc::Rc};

use failure::Fallible;

use crate::{
    common::crypto::hash::Hash,
    storage::mkvs::urkel::{marshal::*, tree::*},
};

/// Size of the encoded round.
const ROUND_SIZE: usize = size_of::<u64>();

impl Marshal for NodeBox {
    fn marshal_binary(&self) -> Fallible<Vec<u8>> {
        match self {
            NodeBox::Internal(ref n) => n.marshal_binary(),
            NodeBox::Leaf(ref n) => n.marshal_binary(),
        }
    }

    fn unmarshal_binary(&mut self, data: &[u8]) -> Fallible<usize> {
        if data.len() < 1 {
            Err(TreeError::MalformedNode.into())
        } else {
            let mut kind = NodeKind::None;
            kind.unmarshal_binary(data)?;
            match kind {
                NodeKind::Internal => {
                    *self = NodeBox::Internal(InternalNode {
                        ..Default::default()
                    });
                }
                NodeKind::Leaf => {
                    *self = NodeBox::Leaf(LeafNode {
                        ..Default::default()
                    });
                }
                _ => {
                    return Err(TreeError::MalformedNode.into());
                }
            };
            match self {
                NodeBox::Internal(ref mut n) => n.unmarshal_binary(data),
                NodeBox::Leaf(ref mut n) => n.unmarshal_binary(data),
            }
        }
    }
}

impl Marshal for NodeKind {
    fn marshal_binary(&self) -> Fallible<Vec<u8>> {
        Ok(vec![*self as u8])
    }

    fn unmarshal_binary(&mut self, data: &[u8]) -> Fallible<usize> {
        if data.len() < 1 {
            Err(TreeError::MalformedNode.into())
        } else {
            if data[0] == NodeKind::None as u8 {
                *self = NodeKind::None;
            } else if data[0] == NodeKind::Internal as u8 {
                *self = NodeKind::Internal;
            } else if data[0] == NodeKind::Leaf as u8 {
                *self = NodeKind::Leaf;
            } else {
                return Err(TreeError::MalformedNode.into());
            }
            Ok(1)
        }
    }
}

impl Marshal for InternalNode {
    fn marshal_binary(&self) -> Fallible<Vec<u8>> {
        let leaf_node_binary: Vec<u8>;
        if self.leaf_node.borrow().is_null() {
            leaf_node_binary = vec![NodeKind::None as u8];
        } else {
            leaf_node_binary =
                noderef_as!(self.leaf_node.borrow().get_node(), Leaf).marshal_binary()?;
        }

        let mut result: Vec<u8> =
            Vec::with_capacity(1 + ROUND_SIZE + leaf_node_binary.len() + 2 * Hash::len());
        result.push(NodeKind::Internal as u8);
        result.append(&mut self.round.marshal_binary()?);
        result.append(&mut self.label_bit_length.marshal_binary()?);
        result.extend_from_slice(&self.label);
        result.extend_from_slice(leaf_node_binary.as_ref());
        result.extend_from_slice(self.left.borrow().hash.as_ref());
        result.extend_from_slice(self.right.borrow().hash.as_ref());

        Ok(result)
    }

    fn unmarshal_binary(&mut self, data: &[u8]) -> Fallible<usize> {
        let mut pos = 0;
        if data.len() < 1 + ROUND_SIZE + size_of::<Depth>() + 1
            || data[pos] != NodeKind::Internal as u8
        {
            return Err(TreeError::MalformedNode.into());
        }
        pos += 1;

        self.round
            .unmarshal_binary(&data[pos..(pos + ROUND_SIZE)])?;
        pos += ROUND_SIZE;

        pos += self.label_bit_length.unmarshal_binary(&data[pos..])?;
        self.label = vec![0; self.label_bit_length.to_bytes()];
        self.label
            .clone_from_slice(&data[pos..pos + self.label_bit_length.to_bytes()]);
        pos += self.label_bit_length.to_bytes();

        if data[pos] == NodeKind::None as u8 {
            self.leaf_node = NodePointer::null_ptr();
            pos += 1;
        } else {
            let mut leaf_node = LeafNode {
                ..Default::default()
            };
            pos += leaf_node.unmarshal_binary(&data[pos..])?;
            self.leaf_node = Rc::new(RefCell::new(NodePointer {
                clean: true,
                hash: leaf_node.get_hash(),
                node: Some(Rc::new(RefCell::new(NodeBox::Leaf(leaf_node)))),
                ..Default::default()
            }));
        };

        // Hashes are only present in non-compact serialization.
        if data.len() >= pos + Hash::len() * 2 {
            let left_hash = Hash::from(&data[pos..pos + Hash::len()]);
            pos += Hash::len();
            let right_hash = Hash::from(&data[pos..pos + Hash::len()]);
            pos += Hash::len();

            if left_hash.is_empty() {
                self.left = NodePointer::null_ptr();
            } else {
                self.left = Rc::new(RefCell::new(NodePointer {
                    clean: true,
                    hash: left_hash,
                    node: None,
                    ..Default::default()
                }));
            }
            if right_hash.is_empty() {
                self.right = NodePointer::null_ptr();
            } else {
                self.right = Rc::new(RefCell::new(NodePointer {
                    clean: true,
                    hash: right_hash,
                    node: None,
                    ..Default::default()
                }));
            }

            self.update_hash();
        }

        self.clean = true;

        Ok(pos)
    }
}

impl Marshal for LeafNode {
    fn marshal_binary(&self) -> Fallible<Vec<u8>> {
        let mut result: Vec<u8> = Vec::with_capacity(1 + ROUND_SIZE + 3 * Hash::len());
        result.push(NodeKind::Leaf as u8);
        result.append(&mut self.round.marshal_binary()?);
        result.append(&mut self.key.marshal_binary()?);
        result.append(&mut self.value.borrow().marshal_binary()?);

        Ok(result)
    }

    fn unmarshal_binary(&mut self, data: &[u8]) -> Fallible<usize> {
        if data.len() < 1 + ROUND_SIZE + size_of::<Depth>() || data[0] != NodeKind::Leaf as u8 {
            return Err(TreeError::MalformedNode.into());
        }

        self.clean = true;

        let mut pos = 1;
        self.round
            .unmarshal_binary(&data[pos..(pos + ROUND_SIZE)])?;
        pos += ROUND_SIZE;

        self.key = Key::new();
        let key_len = self.key.unmarshal_binary(&data[pos..])?;
        pos += key_len;

        self.value = Rc::new(RefCell::new(ValuePointer {
            ..Default::default()
        }));
        let value_len = self.value.borrow_mut().unmarshal_binary(&data[pos..])?;
        pos += value_len;

        self.update_hash();

        Ok(pos)
    }
}

impl Marshal for Key {
    fn marshal_binary(&self) -> Fallible<Vec<u8>> {
        let mut result: Vec<u8> = Vec::new();
        result.append(&mut (self.len() as Depth).marshal_binary()?);
        result.extend_from_slice(self);
        Ok(result)
    }
    fn unmarshal_binary(&mut self, data: &[u8]) -> Fallible<usize> {
        if data.len() < size_of::<Depth>() {
            return Err(TreeError::MalformedKey.into());
        }
        let mut key_len: Depth = 0;
        key_len.unmarshal_binary(data)?;

        if data.len() < size_of::<Depth>() + key_len as usize {
            return Err(TreeError::MalformedKey.into());
        }

        self.extend_from_slice(&data[size_of::<Depth>()..(size_of::<Depth>() + key_len as usize)]);
        Ok(size_of::<Depth>() + key_len as usize)
    }
}

impl Marshal for ValuePointer {
    fn marshal_binary(&self) -> Fallible<Vec<u8>> {
        let mut result: Vec<u8> = Vec::new();
        let value_len = match self.value {
            None => 0,
            Some(ref v) => v.len(),
        };
        result.append(&mut (value_len as u32).marshal_binary()?);
        if let Some(ref v) = self.value {
            result.extend_from_slice(v.as_ref());
        }
        Ok(result)
    }

    fn unmarshal_binary(&mut self, data: &[u8]) -> Fallible<usize> {
        if data.len() < 4 {
            return Err(TreeError::MalformedNode.into());
        }

        let mut value_len = 0u32;
        value_len.unmarshal_binary(data)?;
        let value_len = value_len as usize;

        if data.len() < 4 + value_len {
            return Err(TreeError::MalformedNode.into());
        }

        self.clean = true;
        self.hash = Hash::default();
        if value_len == 0 {
            self.value = None;
        } else {
            self.value = Some(data[4..(4 + value_len)].to_vec());
        }
        self.update_hash();
        Ok(4 + value_len)
    }
}
