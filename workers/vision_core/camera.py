"""Background feature alignment for camera pans/zooms; no identity inference."""
import cv2
import numpy as np


class CameraMotion:
    def __init__(self):
        self.previous=None
        self.orb=cv2.ORB_create(nfeatures=700,fastThreshold=12)

    def update(self,image):
        height,width=image.shape[:2];ratio=min(1,640/width)
        gray=cv2.cvtColor(cv2.resize(image,(round(width*ratio),round(height*ratio))),cv2.COLOR_BGR2GRAY)
        keypoints,descriptors=self.orb.detectAndCompute(gray,None)
        current=(gray,keypoints,descriptors)
        previous=self.previous;self.previous=current
        if previous is None:return None,False
        if descriptors is None or previous[2] is None:
            return None,float(cv2.absdiff(previous[0],gray).mean())>45
        matches=cv2.BFMatcher(cv2.NORM_HAMMING).knnMatch(previous[2],descriptors,k=2)
        good=[pair[0] for pair in matches if len(pair)==2 and pair[0].distance<.72*pair[1].distance]
        if len(good)>=16:
            old=np.float32([previous[1][x.queryIdx].pt for x in good]);new=np.float32([keypoints[x.trainIdx].pt for x in good])
            matrix,inliers=cv2.estimateAffinePartial2D(old,new,method=cv2.RANSAC,ransacReprojThreshold=3)
            if matrix is not None and inliers.sum()>=12 and inliers.mean()>.45:
                scale=np.hypot(matrix[0,0],matrix[0,1]);matrix[:,2]/=ratio
                if .8<scale<1.25 and np.linalg.norm(matrix[:,2])<.4*width:return matrix,False
        difference=float(cv2.absdiff(previous[0],gray).mean())
        return None,difference>45


def transform_point(point,matrix):
    return np.asarray(matrix)[:,:2]@point+np.asarray(matrix)[:,2]


def transform_box(box,matrix):
    x1,y1,x2,y2=box
    points=np.array([transform_point(p,matrix) for p in [(x1,y1),(x2,y1),(x1,y2),(x2,y2)]])
    return [*points.min(axis=0).tolist(),*points.max(axis=0).tolist()]
