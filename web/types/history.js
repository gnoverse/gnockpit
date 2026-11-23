// The /api/history answer, as its JSON tags write it: an omitempty or
// omitzero field is optional.

/**
 * historyReport (web/history.go): the /api/history answer, without the
 * parts of the questions the page never asks: blocks and valsets
 * (?from=&to=), and vote_delays (?days=).
 * @typedef {object} HistoryReport
 * @property {string} now
 * @property {string} chain
 * @property {HistoryFilled} filled
 * @property {HistoryChange[]} [changes]
 * @property {number} [next_before]
 */

/**
 * historyChange (web/history.go): a validator set change against the set
 * of the block below.
 * @typedef {object} HistoryChange
 * @property {number} height
 * @property {string} time
 * @property {boolean} [genesis]
 * @property {HistoryChangeMember[]} added
 * @property {HistoryChangeMember[]} removed
 * @property {HistoryPowerChange[]} repowered
 * @property {HistoryRotation[]} rotated
 * @property {number} size_before
 * @property {number} size_after
 * @property {number} power_before
 * @property {number} power_after
 * @property {HistoryEvent[]} events
 * @property {boolean} events_pending
 */

/**
 * historyChangeMember (web/history.go).
 * @typedef {object} HistoryChangeMember
 * @property {string} address
 * @property {string} [moniker]
 * @property {number} power
 */

/**
 * historyPowerChange (web/history.go).
 * @typedef {object} HistoryPowerChange
 * @property {string} address
 * @property {string} [moniker]
 * @property {number} before
 * @property {number} after
 */

/**
 * historyRotation (web/history.go).
 * @typedef {object} HistoryRotation
 * @property {string} operator
 * @property {string} old_address
 * @property {string} new_address
 * @property {string} [moniker]
 * @property {number} power
 */

/**
 * historyFilled (web/history.go): how far the block history reaches.
 * @typedef {object} HistoryFilled
 * @property {number} from
 * @property {string} [from_time]
 * @property {number} to
 * @property {number} [floor]
 * @property {boolean} complete
 * @property {number} [rate]
 */

/**
 * historyEvent (web/history.go).
 * @typedef {object} HistoryEvent
 * @property {string} type
 * @property {string} operator
 * @property {string} [address]
 * @property {string} [old_address]
 * @property {string} [new_address]
 * @property {number} [power]
 * @property {boolean} applied
 */

export {}
