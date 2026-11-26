// The server's answers on /ws, /api/boot, /api/links and /api/ranking, as
// their JSON tags write them: an omitempty or omitzero field is optional, a
// pointer without omitempty is nullable, and so is a slice the server can
// leave nil.

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
 * @property {boolean} ranking whether /api/ranking serves a ranking
 */

/**
 * rankingAnswer (web/ranking.go): the /api/ranking answer.
 * @typedef {object} RankingAnswer
 * @property {State} state
 * @property {boolean} hide_excluded
 * @property {Published | null} ranking the latest day published; null
 *   while none is
 */

/**
 * ranking.State (ranking/gather.go): the ranking's readiness. left_s is
 * its MarshalJSON's encoding of Left.
 * @typedef {object} State
 * @property {'ready' | 'filling' | 'unavailable'} kind
 * @property {number} stored_share
 * @property {number} left_s
 * @property {boolean} events_pending
 * @property {number} [floor]
 * @property {string} [floor_time]
 */

/**
 * ranking.Published (ranking/store.go), its embedded ranking.Result
 * (ranking/rank.go) inline.
 * @typedef {object} Published
 * @property {string} chain
 * @property {string} day
 * @property {string} computed_at
 * @property {string[]} exclusions
 * @property {number} formula
 * @property {string} first_day
 * @property {string} last_day
 * @property {number} first_height
 * @property {number} last_height
 * @property {Entry[]} entries
 * @property {number[]} proposals_disabled
 */

/**
 * ranking.Entry (ranking/rank.go): an operator's score and place.
 * @typedef {object} Entry
 * @property {string} operator
 * @property {string} name
 * @property {string[]} keys
 * @property {'ranked' | 'not_ranked_yet' | 'excluded'} status
 * @property {number} rank
 * @property {number} hours_in_set
 * @property {number} score
 * @property {Part} uptime
 * @property {Part} outages
 * @property {Part} recovery
 * @property {Part} proposals
 * @property {string[]} tied_with
 * @property {Inputs} inputs
 */

/**
 * ranking.Part (ranking/rank.go): a sub-score's part of a score.
 * @typedef {object} Part
 * @property {number | null} sub
 * @property {number} weight
 * @property {number} points
 */

/**
 * ranking.Inputs (ranking/rank.go): an operator's raw figures.
 * @typedef {object} Inputs
 * @property {number} in_set
 * @property {number} recovering
 * @property {number} signed
 * @property {number} nil
 * @property {number} absent
 * @property {number} other_block
 * @property {Run[]} outages
 * @property {number} longest_absence
 * @property {Sample[]} recovery
 * @property {number | null} recovery_median_ms
 * @property {number} proposals_expected
 * @property {number} proposals_missed
 */

/**
 * ranking.Run (ranking/rank.go): an outage.
 * @typedef {object} Run
 * @property {number} first
 * @property {number} last
 * @property {string} first_time
 * @property {string} end_time
 */

/**
 * ranking.Sample (ranking/rank.go): a recovery sample.
 * @typedef {object} Sample
 * @property {number} restart_height
 * @property {string} restart_time
 * @property {number} duration_ms
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
