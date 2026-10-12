"""Automatic occupied-seat suggestions and visible doorway candidates.

Seat zones represent stable seated positions, not a complete furniture floor plan.
Manual regions take precedence. Predictions retain sources for correction.
"""
import numpy as np
from .behavior import midpoint,iou
from .config import Rect
from .camera import transform_point,transform_box


def normalized(box,width,height):
    x1,y1,x2,y2=box
    return Rect(x1=max(0,min(.999,x1/width)),y1=max(0,min(.999,y1/height)),
                x2=max(.001,min(1,x2/width)),y2=max(.001,min(1,y2/height)))


class SeatMap:
    def __init__(self,manual,width,height,automatic=True):
        self.width,self.height=width,height
        self.manual=bool(manual);self.automatic=automatic
        self.seats=[{'seat_id':f'S{i+1:03d}','region':r.model_dump(),'head_anchor':None,'source':'manual','confidence':1.0} for i,r in enumerate(manual)]
        self.candidates={};self.last_occupied={}

    def compensate_camera(self,matrix):
        if self.manual:return
        scale=float(np.hypot(matrix[0,0],matrix[0,1]))
        for seat in self.seats:
            a=seat['head_anchor']
            if not a:continue
            a['centre']=transform_point(a['centre'],matrix).tolist()
            a['size']=(np.array(a['size'])*scale).tolist()
            r=seat['region'];box=transform_box([r['x1']*self.width,r['y1']*self.height,r['x2']*self.width,r['y2']*self.height],matrix)
            seat['visible']=box[2]>0 and box[3]>0 and box[0]<self.width and box[1]<self.height
            if seat['visible']:seat['region']=normalized(box,self.width,self.height).model_dump()
        for track,history in self.candidates.items():
            self.candidates[track]=[(ms,transform_point(p,matrix).tolist(),(np.array(size)*scale).tolist()) for ms,p,size in history]

    def observe(self,track,head,anchor,timestamp,standing=None):
        point=np.array(anchor)
        if self.manual:
            if track.seat is None:
                for i,seat in enumerate(self.seats):
                    r=seat['region']
                    if r['x1']<=point[0]/self.width<=r['x2'] and r['y1']<=point[1]/self.height<=r['y2']:
                        track.seat=i;break
            return
        if not self.automatic:return
        if track.seat is not None:
            self.last_occupied[track.seat]=timestamp
            return
        if standing is True:return
        # Stable head centres are more reliable than partially occluded shoulders.
        centre=np.array(midpoint(head));w,h=head[2]-head[0],head[3]-head[1]
        for i,seat in enumerate(self.seats):
            if self.last_occupied.get(i)==timestamp:continue
            a=seat['head_anchor']
            if a and np.linalg.norm((centre-a['centre'])/np.maximum(a['size'],8))<.65:
                track.seat=i;self.last_occupied[i]=timestamp;return
        history=self.candidates.setdefault(track.id,[])
        history.append((timestamp,centre.tolist(),[w,h]))
        history[:]=history[-16:]
        if len(history)<4 or timestamp-history[0][0]<1500:return
        centres=np.array([x[1] for x in history]);sizes=np.array([x[2] for x in history])
        median=np.median(centres,axis=0);size=np.median(sizes,axis=0)
        if np.max(np.linalg.norm((centres-median)/np.maximum(size,8),axis=1))>.55:return
        # A moving person spanning many positions won't establish a stable seat.
        box=[median[0]-.95*size[0],median[1]-.8*size[1],median[0]+.95*size[0],median[1]+2.3*size[1]]
        if len(self.seats)>=80:return
        region=normalized(box,self.width,self.height)
        track.seat=len(self.seats)
        self.seats.append({'seat_id':f'S{len(self.seats)+1:03d}','region':region.model_dump(),
                          'head_anchor':{'centre':median.tolist(),'size':size.tolist()},'source':'stable-person-position',
                          'confidence':round(min(.9,.55+.025*len(history)),3),'established_ms':timestamp})
        self.last_occupied[track.seat]=timestamp

    def departure(self,track,head,anchor,standing=None):
        if track.seat is None:return None
        seat=self.seats[track.seat]
        if self.manual:
            r=seat['region'];x,y=anchor[0]/self.width,anchor[1]/self.height
            return not(r['x1']<=x<=r['x2'] and r['y1']<=y<=r['y2'])
        if standing is None:return None
        if standing is False:return False
        reference=seat['head_anchor'];centre=np.array(midpoint(head));size=np.array(reference['size'])
        displacement=(centre-reference['centre'])/np.maximum(size,8)
        # Looking down/up shifts a head vertically. Require lateral displacement too.
        return bool(abs(displacement[0])>1.1 and np.linalg.norm(displacement)>1.5)

    def report(self,room_objects,manual_door):
        doors=[d for d in room_objects if d['kind'] in ('door','doorway')]
        entrances=[{'region':normalized(d['bbox'],self.width,self.height).model_dump(),'confidence':d['confidence'],'kind':d['kind']} for d in doors]
        if isinstance(manual_door,Rect):entrances=[{'region':manual_door.model_dump(),'confidence':1.0,'kind':'doorway','source':'manual'}]
        return {'seats':self.seats,'seat_status':'manual' if self.manual else 'estimated_occupied_positions' if self.seats else 'insufficient',
                'seat_note':'自动区域根据多帧稳定人员位置估计，仅覆盖已观察到的占用位置；空座位及原地静站人员可能判断不全，区域可手动修正。',
                'entrances':entrances,
                'entrance_status':'manual' if manual_door else 'candidate_found' if doors else 'not_detected',
                'entrance_note':'门/通道模型只判断可见入口候选。未检出不证明没有入口；没有课堂时间不判断迟到早退。'}
