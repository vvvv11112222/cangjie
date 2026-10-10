from workers.tools.check_vision_quality import measure_frame


def test_evaluation_unknown_is_missed_and_extra_raise_is_false_positive():
    counters = {"hand_raise": dict(tp=0, fp=0, fn=0, labelled=0, unknown_or_missed=0)}
    labels = [{"bbox": [0, 0, 10, 10], "behaviors": {"hand_raise": True}}]
    predictions = [
        {"bbox": [0, 0, 10, 10], "behaviors": {"hand_raise": None}},
        {"bbox": [20, 20, 30, 30], "behaviors": {"hand_raise": True}}]
    measure_frame(labels, predictions, counters)
    assert counters["hand_raise"] == dict(tp=0, fp=1, fn=1, labelled=1, unknown_or_missed=1)
