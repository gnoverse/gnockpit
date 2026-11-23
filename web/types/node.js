// The node package's types the server sends the page, as their JSON
// tags write them: an omitempty field is optional, a pointer without
// omitempty is nullable, and so is a slice the server can leave nil.

/**
 * node.Status (node/types.go): the node's current status.
 * @typedef {object} Status
 * @property {NodeInfo} node_info
 * @property {SyncInfo} sync_info
 * @property {ValInfo} validator_info
 */

/**
 * node.NodeInfo (node/types.go).
 * @typedef {object} NodeInfo
 * @property {string} moniker
 * @property {string} network
 * @property {string} id
 * @property {string} net_address
 */

/**
 * node.SyncInfo (node/types.go).
 * @typedef {object} SyncInfo
 * @property {string} latest_block_height
 * @property {string} latest_block_time
 * @property {boolean} catching_up
 */

/**
 * node.ValInfo (node/types.go).
 * @typedef {object} ValInfo
 * @property {string} address
 * @property {PubKey} pub_key
 */

/**
 * node.PubKey (node/types.go), with the bech32 its MarshalJSON adds.
 * @typedef {{ type: string, '@type': string, value: string, bech32?: string }} PubKey
 */

/**
 * node.Peer (node/types.go): a connected peer.
 * @typedef {object} Peer
 * @property {string} moniker
 * @property {string} remote_ip
 * @property {string} node_id
 * @property {string} [p2p_address]
 * @property {string} [external_address]
 * @property {string} [rpc_url]
 * @property {boolean} [catching_up]
 * @property {string} [val_address]
 * @property {string} [val_pubkey]
 * @property {string} [role]
 * @property {string} [height]
 * @property {string} [round]
 * @property {string} [step]
 * @property {string} [error]
 * @property {string} [first_seen]
 * @property {string} [last_seen]
 * @property {number} [n_peers]
 * @property {string} [peer_height]
 * @property {string} [peer_round]
 * @property {string} [peer_step]
 * @property {boolean} [has_proposal]
 * @property {string} [peer_prevotes]
 * @property {string} [peer_precommits]
 * @property {number} [lat]
 * @property {number} [lon]
 * @property {string} [city]
 * @property {string} [country]
 * @property {number} [asn]
 * @property {string} [as_org]
 * @property {string} [provider]
 * @property {boolean} [source]
 */

/**
 * node.ConsensusConfig (node/types.go): the consensus timeouts.
 * @typedef {object} ConsensusConfig
 * @property {string} timeout_propose
 * @property {string} timeout_prevote
 * @property {string} timeout_precommit
 * @property {string} timeout_commit
 * @property {boolean} create_empty_blocks
 */

/**
 * node.VoteInfo (node/types.go): a validator's votes and signing.
 * @typedef {object} VoteInfo
 * @property {number} index
 * @property {string} address
 * @property {string} name
 * @property {string} [pub_key]
 * @property {string} [voting_power]
 * @property {boolean} prevoted
 * @property {boolean} precommit
 * @property {number} missed_100
 * @property {boolean} [inactive]
 * @property {number} missed_24h
 * @property {number} nil_100
 * @property {number} other_block_100
 * @property {number | null} vote_delay_ms
 * @property {number} missed_proposals_24h
 * @property {number} expected_proposals_24h
 */

/**
 * node.VotesReport (node/types.go): the prevote and precommit state.
 * @typedef {object} VotesReport
 * @property {string} height
 * @property {string} round
 * @property {string} step
 * @property {string} proposer
 * @property {string} [round_start_time]
 * @property {ConsensusConfig} [config]
 * @property {VoteInfo[] | null} validators
 * @property {string} [missed_since]
 * @property {string} timestamp
 */

/**
 * node.SystemInfo (node/types.go).
 * @typedef {object} SystemInfo
 * @property {string} node_time
 * @property {string} [genesis_time]
 */

/**
 * node.MissingValidator (node/types.go): a validator that did not sign
 * a block.
 * @typedef {object} MissingValidator
 * @property {string} name
 * @property {string} address
 */

/**
 * node.BlockInfo (node/types.go): the signing of a recent block.
 * @typedef {object} BlockInfo
 * @property {string} height
 * @property {string} time
 * @property {number} signers
 * @property {number} total
 * @property {MissingValidator[]} [missing]
 * @property {string} proposer
 * @property {number} block_ms
 * @property {string} app_hash
 * @property {number} num_txs
 */

/**
 * node.ValSigning (node/signing.go), which has no JSON tags: its fields
 * go by their Go names.
 * @typedef {object} ValSigning
 * @property {number} From
 * @property {number} Signed
 * @property {number} Eligible
 * @property {number} Missed
 * @property {number} MissedInARow
 * @property {number} SignedInARow
 * @property {number} Nil
 * @property {number} OtherBlock
 * @property {number} Votes
 * @property {number} VoteDelayMs
 */

/**
 * node.SigningStats (node/types.go): the validators' signing over the
 * recent blocks.
 * @typedef {object} SigningStats
 * @property {number} window_size
 * @property {number} active_count
 * @property {number} total_count
 * @property {number} max_missed_in_a_row
 * @property {number} bft_threshold
 * @property {number} margin
 * @property {boolean} can_add_one
 * @property {number} avg_block_ms
 * @property {BlockInfo[] | null} recent_blocks
 * @property {Record<string, ValSigning>} validator_signing
 */

export {}
