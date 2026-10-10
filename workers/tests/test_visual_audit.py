from workers.tools.audit_visual_report import audit


def test_audit_keeps_unknown_separate_from_negative_and_uses_pixel_boundaries():
    def observation(size, hand, head):
        return {"bbox": [0, 0, size, 40], "seat_id": "S001",
                "behaviors": {"hand_raise": hand, "head_down": head}}
    report = {"metadata": {"sha256": "x", "width": 640, "height": 360},
              "counts": [{"visible_count": 3}, {"visible_count": None}],
              "persons": [{"track_id": "T0001", "observations": [observation(15, None, None)]},
                          {"track_id": "T0002", "observations": [observation(16, False, True),
                                                                   observation(32, True, False)]}]}
    result = audit(report)
    groups = result["by_head_pixels"]
    assert groups["small_head_under_16px"]["hand_evaluable_ratio"] == 0
    assert groups["head_16_to_32px"]["head_positive"] == 1
    assert groups["head_at_least_32px"]["hand_positive"] == 1
    assert result["visible_count_mean"] == 3
    assert result["seats_associated_with_multiple_tracks"] == 1
    assert result["tracks_with_only_one_observation"] == 1


def test_audit_does_not_invent_ratios_for_missing_observations():
    report = {"metadata": {"sha256": "x", "width": 640, "height": 360},
              "counts": [{"visible_count": None}], "persons": []}
    result = audit(report)
    assert result["visible_count_mean"] is None
    assert result["anonymous_track_count"] == 0
    assert all(group["hand_evaluable_ratio"] is None for group in result["by_head_pixels"].values())
