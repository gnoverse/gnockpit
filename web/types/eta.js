// The /api/eta answers, as their JSON tags write them: an omitempty field
// is optional, a pointer without omitempty is nullable. Height mode fills
// target and the eta fields; time mode fills estimate.

/**
 * etaReport (web/eta.go): the /api/eta answer.
 * @typedef {object} EtaReport
 * @property {string} chain
 * @property {EtaBlock | null} now
 * @property {EtaTarget} [target]
 * @property {string} [eta]
 * @property {number} [seconds_left]
 * @property {string} [reached_at]
 * @property {EtaTimeRange} [range_80]
 * @property {EtaTimeRange} [range_95]
 * @property {EtaHeightEstimate} [estimate]
 * @property {EtaPace} [pace]
 * @property {EtaOriginal} [original]
 * @property {EtaTrailPoint[]} [trail]
 * @property {EtaScenario[]} [scenarios]
 * @property {EtaHalt | null} halt
 * @property {string} [warning]
 * @property {EtaHistory} [history]
 */

/**
 * etaBlock (web/eta.go).
 * @typedef {object} EtaBlock
 * @property {number} height
 * @property {string} block_time
 */

/**
 * etaTarget (web/eta.go).
 * @typedef {object} EtaTarget
 * @property {number} height
 * @property {number} blocks_left
 */

/**
 * etaTimeRange (web/eta.go).
 * @typedef {object} EtaTimeRange
 * @property {string} from
 * @property {string} to
 */

/**
 * etaHeightRange (web/eta.go).
 * @typedef {object} EtaHeightRange
 * @property {number} from
 * @property {number} to
 */

/**
 * etaHeightEstimate (web/eta.go).
 * @typedef {object} EtaHeightEstimate
 * @property {string} at
 * @property {number} height
 * @property {EtaHeightRange} [range_80]
 * @property {EtaHeightRange} [range_95]
 */

/**
 * etaPace (web/eta.go).
 * @typedef {object} EtaPace
 * @property {number} seconds_per_block
 * @property {number} over_blocks
 */

/**
 * etaOriginal (web/eta.go): the estimate as of since.
 * @typedef {object} EtaOriginal
 * @property {string} as_of
 * @property {number} height
 * @property {string} eta
 * @property {number} drift_seconds
 */

/**
 * etaTrailPoint (web/eta.go).
 * @typedef {object} EtaTrailPoint
 * @property {string} at
 * @property {number} height
 * @property {string} eta
 * @property {EtaTimeRange} [range_80]
 */

/**
 * etaScenario (web/eta.go): the answer at the pace of a look-back span.
 * @typedef {object} EtaScenario
 * @property {string} pace_of
 * @property {number} seconds_per_block
 * @property {string} [eta]
 * @property {number} [height]
 */

/**
 * etaHalt (web/eta.go): the scheduled halt.
 * @typedef {object} EtaHalt
 * @property {number} height
 * @property {string} min_version
 * @property {string} first_seen
 */

/**
 * etaHistory (web/eta.go): how far the block-time history reaches.
 * @typedef {object} EtaHistory
 * @property {string} [from]
 * @property {number} samples
 * @property {boolean} complete
 */

/**
 * etaErrorBody (web/eta.go): an /api/eta error, naming the scheduled halt
 * when the question was the halt's.
 * @typedef {object} EtaErrorBody
 * @property {string} error
 * @property {EtaHalt} [halt]
 */

export {}
