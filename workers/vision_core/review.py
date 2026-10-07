"""Versioned observation reviews. Automatic facts are never overwritten."""
KINDS=('hand_raise','head_down','leave_seat','possible_phone')


def review_observation(report, track_id, timestamp, kind, value, reason, reviewer, revision, reviewed_at):
    if revision != report.get('review_revision',0):
        raise RuntimeError('报告复核版本已变化，请刷新后重试')
    person=next((p for p in report.get('persons',[]) if p['track_id']==track_id),None)
    observation=next((o for o in person['observations'] if o['timestamp_ms']==timestamp),None) if person else None
    if observation is None:raise LookupError('该人物没有这个采样帧')
    record={'event_type':kind,'value':value,'reason':reason,'reviewer':reviewer,
            'reviewed_at':reviewed_at,'revision':revision+1,
            'model_value':observation['behaviors'][kind]}
    observation.setdefault('reviews',{})[kind]=record
    report.setdefault('review_history',[]).append({'track_id':track_id,'timestamp_ms':timestamp,**record})
    report['review_revision']=revision+1
    return record


def review_summary(report):
    records=[(o,k,r) for p in report.get('persons',[]) for o in p['observations'] for k,r in o.get('reviews',{}).items()]
    return {'reviewed_observations':len(records),
            'confirmed_positive':sum(r['value'] is True for _,_,r in records),
            'corrected_observations':sum(r['value'] != o['behaviors'][k] for o,k,r in records),
            'uncertain_observations':sum(r['value'] is None for _,_,r in records)}
