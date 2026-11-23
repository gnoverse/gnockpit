// The server's answers on /ws, /api/boot and /api/links, as their JSON
// tags write them: an omitempty field is optional, a pointer without
// omitempty is nullable, and so is a slice the server can leave nil.

/** @import { Peer, SigningStats, Status, SystemInfo, VotesReport } from './node.js' */

/**
 * wsMsg (web/server.go): a /ws message, the snapshot a client gets on
 * connecting or the update each publish broadcasts.
 * @typedef {object} WsMsg
 * @property {'snapshot' | 'update'} type
 * @property {LiveData} data
 */

/**
 * liveData (web/server.go): the snapshot as the page shows it.
 * @typedef {object} LiveData
 * @property {string} time
 * @property {Status | null} status
 * @property {Peer[] | null} peers
 * @property {VotesReport | null} votes
 * @property {CheckData} checks
 * @property {SigningStats} [signing]
 */

/**
 * checkData (web/server.go).
 * @typedef {object} CheckData
 * @property {string} [apphash_last]
 * @property {string} [val_address]
 * @property {string} [val_pubkey]
 * @property {SystemInfo | null} system
 * @property {boolean} [hide_sources]
 */

/**
 * bootStatus (web/server.go, handleBootStatus): the /api/boot answer.
 * @typedef {object} BootStatus
 * @property {boolean} rpc
 * @property {string} [chain_name]
 */

/**
 * Link (web/links.go): a header link.
 * @typedef {object} Link
 * @property {string} title
 * @property {string} url
 */

/**
 * statusLinkOut (web/links.go, handleLinks): a status link and its state.
 * @typedef {object} StatusLinkOut
 * @property {string} title
 * @property {string} url
 * @property {string} state
 */

/**
 * The /api/links answer, handleLinks' unnamed struct (web/links.go).
 * @typedef {object} LinksAnswer
 * @property {Link[]} links
 * @property {StatusLinkOut[]} statusLinks
 */

export {}
