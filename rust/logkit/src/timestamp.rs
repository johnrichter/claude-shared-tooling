//! RFC 3339 UTC millisecond timestamps, per
//! `schemas/logkit/logkit.contract.json`'s `timestamp` block: fixed width,
//! zero-padded, truncated (never rounded) to three fractional digits.

use time::OffsetDateTime;

/// The emitter's current wall-clock instant, rendered canonically.
#[must_use]
pub fn now() -> String {
    render(OffsetDateTime::now_utc())
}

/// Renders an instant as `YYYY-MM-DDTHH:MM:SS.sssZ`. The instant is first
/// converted to UTC (a no-op if it already is one), so a caller can never
/// hand this a local or offset-bearing reading and get one back.
///
/// `OffsetDateTime::millisecond` truncates toward zero already (integer
/// division of the nanosecond field), matching the contract's
/// `rounding: truncate-toward-zero` — a finer clock reading is never
/// rounded up into the next millisecond.
#[must_use]
pub fn render(instant: OffsetDateTime) -> String {
    let utc = instant.to_offset(time::UtcOffset::UTC);
    format!(
        "{:04}-{:02}-{:02}T{:02}:{:02}:{:02}.{:03}Z",
        utc.year(),
        u8::from(utc.month()),
        utc.day(),
        utc.hour(),
        utc.minute(),
        utc.second(),
        utc.millisecond(),
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use time::macros::datetime;

    #[test]
    fn renders_fixed_width_utc_millisecond() {
        let instant = datetime!(2026-07-26 09:41:07.480 UTC);
        assert_eq!(render(instant), "2026-07-26T09:41:07.480Z");
    }

    #[test]
    fn zero_pads_every_field() {
        let instant = datetime!(2026-01-02 03:04:05.006 UTC);
        assert_eq!(render(instant), "2026-01-02T03:04:05.006Z");
    }

    #[test]
    fn truncates_sub_millisecond_precision_toward_zero() {
        // 480_999_999ns is 480ms plus change; truncation keeps it 480, a
        // round-to-nearest would have produced 481.
        let instant = datetime!(2026-07-26 09:41:07.480_999_999 UTC);
        assert_eq!(render(instant), "2026-07-26T09:41:07.480Z");
    }

    #[test]
    fn converts_a_non_utc_offset_to_utc() {
        let instant = datetime!(2026-07-26 05:41:07.480 -4);
        assert_eq!(render(instant), "2026-07-26T09:41:07.480Z");
    }

    #[test]
    fn now_produces_the_canonical_fixed_width() {
        assert_eq!(now().len(), 24);
        assert!(now().ends_with('Z'));
    }
}
