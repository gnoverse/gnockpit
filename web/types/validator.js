// The /api/validator answer, as its JSON tags write it: an omitempty field
// is optional, a pointer without omitempty is nullable, and an embedded
// struct's fields are inline.

/**
 * validatorReport (web/validator.go): the /api/validator answer.
 * @typedef {object} ValidatorReport
 * @property {string} chain
 * @property {string} now
 * @property {string} address
 * @property {string} operator
 * @property {string} moniker
 * @property {ValidatorStatus} [status]
 * @property {ValidatorKey[]} keys
 * @property {ValidatorLocation} [location]
 * @property {ValidatorProfile} [profile]
 * @property {TimelineEntry[]} timeline
 * @property {number[]} vote_delay_edges_ms
 * @property {ValidatorDay[]} days
 * @property {ValidatorRecovery[]} recoveries
 * @property {ValidatorSummary} summary
 * @property {ValidatorLive} live
 */

/**
 * validatorStatus (web/validator.go): the validator's place in the node's
 * current set.
 * @typedef {object} ValidatorStatus
 * @property {boolean} in_set
 * @property {number} [since_height]
 * @property {string} [since_time]
 * @property {boolean} [since_before]
 * @property {string} [signing_address]
 * @property {number | null} power
 * @property {number | null} share
 */

/**
 * validatorKey (web/validator.go).
 * @typedef {object} ValidatorKey
 * @property {string} address
 * @property {number} from
 * @property {number} [to]
 */

/**
 * validatorLocation (web/validator.go).
 * @typedef {object} ValidatorLocation
 * @property {string} [country]
 * @property {string} [city]
 * @property {string} [provider]
 */

/**
 * validatorProfile (web/validator.go): the operator's valopers profile.
 * @typedef {object} ValidatorProfile
 * @property {string} moniker
 * @property {string} description
 * @property {string} server_type
 * @property {string} operator
 * @property {string} signing_address
 * @property {string} signing_pubkey
 * @property {boolean} [keep_running]
 * @property {number} [last_rotation_height]
 */

/**
 * signingCounts (web/validator.go): a validator's counters over a span of
 * blocks.
 * @typedef {object} SigningCounts
 * @property {number} in_set
 * @property {number} signed
 * @property {number} absent
 * @property {number} nil
 * @property {number} other_block
 * @property {number} proposals_expected
 * @property {number} proposals_missed
 * @property {number} votes
 * @property {number} vote_delay_mean_ms
 */

/**
 * validatorDay (web/validator.go): a UTC day's counters.
 * @typedef {SigningCounts & { day: string, vote_delay_buckets: number[] }} ValidatorDay
 */

/**
 * validatorRecovery (web/validator.go): a recovery after a restart.
 * @typedef {object} ValidatorRecovery
 * @property {number} restart_height
 * @property {string} restart_time
 * @property {number} signed_at
 * @property {number} duration_ms
 * @property {boolean} pending
 * @property {boolean} scored
 */

/**
 * validatorSummary (web/validator.go): the raw figures over the scoring
 * window.
 * @typedef {SigningCounts & {
 *     first_day: string,
 *     last_day: string,
 *     outages: number,
 *     recovery: ValidatorRecoverySummary,
 * }} ValidatorSummary
 */

/**
 * validatorRecoverySummary (web/validator.go).
 * @typedef {object} ValidatorRecoverySummary
 * @property {number} samples
 * @property {number} median_ms
 * @property {number} slowest_ms
 */

/**
 * validatorLive (web/validator.go): the live window's figures of the
 * validator's key.
 * @typedef {object} ValidatorLive
 * @property {number | null} vote_delay_ms
 * @property {number} missed_100
 * @property {number} nil_100
 * @property {number} other_block_100
 * @property {number} missed_24h
 * @property {number} missed_proposals_24h
 * @property {number} expected_proposals_24h
 */

/**
 * timelineEntry (web/validator_timeline.go): one entry of a validator's
 * timeline, its kind's part inline and the other kinds' absent, as
 * setChangeEntries and historyEntries build them; a recovered entry has
 * its duration, a recovering one none.
 * @typedef {{ height: number, time: string } & (
 *     | ({ kind: 'recovered' } & TimelineRecovery & { duration_ms: number })
 *     | ({ kind: 'recovering' } & TimelineRecovery)
 *     | ({ kind: 'outage' } & TimelineOutage)
 *     | ({ kind: 'power' } & TimelinePower)
 *     | ({ kind: 'rotated' } & TimelineRotation)
 *     | ({ kind: 'joined' | 'left' | 'genesis' } & TimelineMembership)
 * )} TimelineEntry
 */

/**
 * timelineRecovery (web/validator_timeline.go).
 * @typedef {object} TimelineRecovery
 * @property {number} restart_height
 * @property {number} [duration_ms]
 */

/**
 * timelineOutage (web/validator_timeline.go).
 * @typedef {object} TimelineOutage
 * @property {number} to
 * @property {number} blocks
 * @property {string} end_time
 * @property {boolean} ongoing
 */

/**
 * timelinePower (web/validator_timeline.go).
 * @typedef {object} TimelinePower
 * @property {number} before
 * @property {number} after
 */

/**
 * timelineRotation (web/validator_timeline.go).
 * @typedef {object} TimelineRotation
 * @property {string} old_address
 * @property {string} new_address
 */

/**
 * timelineMembership (web/validator_timeline.go).
 * @typedef {object} TimelineMembership
 * @property {number} power
 */

export {}
