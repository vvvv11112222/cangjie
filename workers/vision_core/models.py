"""CPU detection and optional GPU pose adapters for the pinned ONNX exports.

Model origins and licensing references are recorded by bootstrap_models.py.
These decoders support the pinned exports, not arbitrary YOLO files.
"""
import json
import os
import ctypes
import threading
import sys
import time
from pathlib import Path
import cv2
cv2.ocl.setUseOpenCL(False)
import numpy as np
accelerator = Path(os.environ.get('VISION_ACCELERATOR_ROOT', Path(__file__).resolve().parents[1]/'accelerators'))/'directml'
runtime_file = Path(os.environ.get('VISION_RUNTIME_CONFIG', Path(__file__).resolve().parents[1]/'config'/'runtime.cpu.json'))
runtime_settings = json.loads(runtime_file.read_text(encoding='utf-8')) if runtime_file.exists() else {}
runtime_kind = os.environ.get('VISION_RUNTIME', runtime_settings.get('engine','directml'))
cuda_path = accelerator.parent/'cuda'
if runtime_kind == 'cuda' and (cuda_path/'onnxruntime').is_dir():
    accelerator = cuda_path
if runtime_kind != 'cpu' and (accelerator/'onnxruntime').is_dir():
    sys.path.insert(0,str(accelerator))
import onnxruntime as ort
_dll_paths=[]
if runtime_kind == 'cuda' and os.name=='nt':
    bins=[]
    for directory in (cuda_path/'nvidia').glob('*/bin'):
        _dll_paths.append(os.add_dll_directory(str(directory)))
        bins.append(str(directory))
    os.environ['PATH']=os.pathsep.join(bins+[os.environ.get('PATH','')])
    nvrtc=cuda_path/'nvidia/cuda_nvrtc/bin/nvrtc64_120_0.dll'
    if nvrtc.exists():ctypes.WinDLL(str(nvrtc))
if runtime_kind == 'cuda' and hasattr(ort, 'preload_dlls'):
    ort.preload_dlls(directory='')
from .config import MODELS
from .media import digest_file
from .devices import selected_device

_instance = None
_lock = threading.Lock()
_session_records = {}
_records_lock = threading.Lock()


def runtime_status():
    with _records_lock:
        records = list(_session_records.values())
    warnings = [r['warning'] for r in records if r.get('warning')]
    if _instance is not None and _instance.provenance.get('batch_fallback'):
        warnings.append('批量加速失败，已改为逐人计算：'+_instance.provenance['batch_fallback'])
    if selected_device()['error']:
        warnings.append('无法确认加速显卡：' + selected_device()['error'])
    return {'device': selected_device()['selected'], 'sessions': records, 'warnings': list(dict.fromkeys(warnings))}
POSE_MEAN=np.array([123.675,116.28,103.53])
POSE_STD=np.array([58.395,57.12,57.375])
POSE_LUT=((np.arange(256,dtype=np.float32)[:,None]-POSE_MEAN)/POSE_STD).astype(np.float32).reshape(256,1,3)
HEAD_LUT=(((np.arange(256,dtype=np.float32)/255)[:,None]-[.485,.456,.406])/[.229,.224,.225]).astype(np.float32).reshape(256,1,3)


def session(path, use_gpu=False, batch_override=None):
    options = ort.SessionOptions()
    options.intra_op_num_threads = 4
    options.inter_op_num_threads = 1
    if batch_override is not None:options.add_free_dimension_override_by_name('batch',batch_override)
    if use_gpu and int(os.environ.get('VISION_GPU_THREADS',runtime_settings.get('gpu_threads',4)))==1:
        options.intra_op_num_threads=1
        options.add_session_config_entry('session.intra_op.allow_spinning','0')
        options.add_session_config_entry('session.inter_op.allow_spinning','0')
    providers=['CPUExecutionProvider']
    warning = None
    if use_gpu and runtime_kind == 'cuda' and 'CUDAExecutionProvider' in ort.get_available_providers():
        providers=[('CUDAExecutionProvider',{'device_id':0,'use_tf32':int(os.environ.get('VISION_TF32',runtime_settings.get('use_tf32',0))),
                    'cudnn_conv_algo_search':os.environ.get('VISION_CUDA_ALGO',runtime_settings.get('cuda_algo','HEURISTIC')),
                    'prefer_nhwc':int(os.environ.get('VISION_NHWC',runtime_settings.get('prefer_nhwc',0))),
                    'arena_extend_strategy':'kSameAsRequested'}),'CPUExecutionProvider']
    elif use_gpu and runtime_kind == 'directml' and 'DmlExecutionProvider' in ort.get_available_providers():
        options.enable_mem_pattern=False
        options.execution_mode=ort.ExecutionMode.ORT_SEQUENTIAL
        device=selected_device()['selected']
        if device:
            providers=[('DmlExecutionProvider',{'device_id':device['device_id']}),'CPUExecutionProvider']
        else:
            warning=f'{path.name}：未找到可确认的显卡，正在使用 CPU'
    elif use_gpu and runtime_kind != 'cpu':
        warning=f'{path.name}：显卡加速不可用，正在使用 CPU'
    try:
        result=ort.InferenceSession(str(path), sess_options=options, providers=providers)
    except Exception as error:
        if providers==['CPUExecutionProvider']:
            raise
        options.enable_mem_pattern=True
        warning=f'{path.name}：显卡启动失败，已改用 CPU（{str(error)[:200]}）'
        result=ort.InferenceSession(str(path),sess_options=options,providers=['CPUExecutionProvider'])
    if use_gpu and runtime_kind != 'cpu' and result.get_providers()[0]=='CPUExecutionProvider' and not warning:
        warning=f'{path.name}：实际未启用显卡，正在使用 CPU'
    if warning:print(warning,flush=True)
    with _records_lock:
        _session_records[(path.name,batch_override)]={'model':path.name,'batch':batch_override,
            'provider':result.get_providers()[0],'gpu_requested':use_gpu,'warning':warning}
    return result


class VisionModels:
    def __init__(self):
        registry = json.loads((MODELS / "manifest.json").read_text(encoding="utf-8"))
        pinned = json.loads((Path(__file__).resolve().parents[1]/'config'/'models.manifest.json').read_text(encoding='utf-8'))
        for name, item in pinned.items():
            if registry.get(name, {}).get('sha256') != item['sha256']:
                raise ValueError(f"模型清单不是团队固定版本：{name}")
        for name, item in registry.items():
            if Path(name).name != name or digest_file(MODELS / name) != item["sha256"]:
                raise ValueError(f"模型摘要不匹配：{name}")
        self.fixed_pose_batches=bool(int(os.environ.get('VISION_FIXED_BATCH',runtime_settings.get('fixed_pose_batches',0))))
        self.decoded_poses=bool(int(os.environ.get('VISION_DECODED_POSES',runtime_settings.get('decoded_poses',0))))
        self.pose_files={False:'rtmpose_s.onnx',True:'rtmpose_x.onnx'}
        if self.decoded_poses:
            for kind,name in list(self.pose_files.items()):
                derived=Path(name).stem+'_decoded.onnx'
                if derived in registry and (MODELS/derived).is_file():self.pose_files[kind]=derived
        self._pose_sessions={}
        self.pose_metrics={}
        self.reuse_pose_buffers=True
        self._pose_buffers={}
        gpu_detection=bool(int(os.environ.get('VISION_GPU_DETECTION',runtime_settings.get('gpu_detection',0))))
        gpu_yolox=bool(int(os.environ.get('VISION_GPU_YOLOX',runtime_settings.get('gpu_yolox',int(gpu_detection)))))
        self.detector = session(MODELS / "yolox_tiny.onnx",use_gpu=gpu_yolox)
        self.pose = session(MODELS / self.pose_files[False],use_gpu=True,batch_override=1 if self.fixed_pose_batches else None)
        self.classroom_detector = session(MODELS / 'yolov11_phd_s.onnx',use_gpu=gpu_detection)
        self.classroom_pose = session(MODELS / self.pose_files[True],use_gpu=True,batch_override=1 if self.fixed_pose_batches else None)
        self.door_detector=session(MODELS/'doorway.onnx') if (MODELS/'doorway.onnx').exists() else None
        self.head_pose=session(MODELS/'headpose_resnet18.onnx',use_gpu=True) if (MODELS/'headpose_resnet18.onnx').exists() else None
        self.pose_batch_size=max(1,min(32,int(os.environ.get('VISION_BATCH_SIZE',runtime_settings.get('pose_batch_size',1)))))
        self.provenance = {"weights": registry, "onnxruntime": ort.__version__, "provider": self.classroom_pose.get_providers()[0],
                           "runtime_status":runtime_status(),
                           "detector_provider":self.classroom_detector.get_providers()[0], "pose_providers":self.classroom_pose.get_providers(),
                           "execution_engine":runtime_kind,"pose_batch_size":self.pose_batch_size,
                           "fixed_pose_batches":self.fixed_pose_batches,
                           "pose_files":self.pose_files,"pose_decode_on_graph":self.decoded_poses,
                           "yolox_provider":self.detector.get_providers()[0],
                           "precision":"float32; CUDA TF32 " + ('enabled' if int(os.environ.get('VISION_TF32',runtime_settings.get('use_tf32',0))) else 'disabled'),
                           "detector_input": [416, 416], "pose_input": [192, 256],
                           "classroom_detector_input": [640, 640], "classroom_pose_input": [288, 384],
                           "classroom_head_color": "RGB", "classroom_head_export": "xywh_scores_class_ids (3 outputs)",
                           "preprocessing_version": "classroom-head-rgb-and-rtmpose-simcc-v2"}
        self.grids = []
        self.strides = []
        for stride in (8, 16, 32):
            y, x = np.meshgrid(np.arange(416 // stride), np.arange(416 // stride), indexing="ij")
            self.grids.append(np.stack((x, y), axis=-1).reshape(-1, 2))
            self.strides.append(np.full((x.size, 1), stride))
        self.grid = np.concatenate(self.grids).astype(np.float32)
        self.stride = np.concatenate(self.strides).astype(np.float32)

    def detect(self, image, threshold, class_ids=(0,67)):
        height, width = image.shape[:2]
        ratio = min(416 / width, 416 / height)
        resized = cv2.resize(image, (int(width * ratio), int(height * ratio)))
        canvas = np.full((416, 416, 3), 114, dtype=np.uint8)
        canvas[:resized.shape[0], :resized.shape[1]] = resized
        tensor = np.ascontiguousarray(canvas.transpose(2, 0, 1)[None], dtype=np.float32)
        output = self.detector.run(None, {self.detector.get_inputs()[0].name: tensor})[0][0].copy()
        if output.shape != (len(self.grid), 85):
            raise ValueError(f"不支持的 YOLOX 导出结构：{output.shape}")
        output[:, :2] = (output[:, :2] + self.grid) * self.stride
        output[:, 2:4] = np.exp(np.clip(output[:, 2:4], -15, 15)) * self.stride
        result = []
        for class_id in class_ids:
            confidence = output[:, 4] * output[:, 5 + class_id]
            selected = np.flatnonzero(confidence >= threshold)
            if not len(selected):
                continue
            boxes = output[selected, :4]
            xy = boxes[:, :2] - boxes[:, 2:] / 2
            nms_boxes = np.concatenate((xy, boxes[:, 2:]), axis=1) / ratio
            kept = cv2.dnn.NMSBoxes(nms_boxes.tolist(), confidence[selected].tolist(), threshold, 0.45)
            for idx in np.asarray(kept).reshape(-1):
                box = nms_boxes[idx]
                coords = [max(0, float(box[0])), max(0, float(box[1])),
                          min(width, float(box[0] + box[2])), min(height, float(box[1] + box[3]))]
                if coords[2] > coords[0] and coords[3] > coords[1]:
                    result.append({"bbox": coords, "confidence": float(confidence[selected[idx]]), "class_id": class_id})
        return result

    def detect_room(self,image,threshold=.35):
        if self.door_detector is None:
            return []
        height,width=image.shape[:2];ratio=min(640/width,640/height)
        rw,rh=int(width*ratio),int(height*ratio);left,top=(640-rw)//2,(640-rh)//2
        canvas=np.full((640,640,3),114,np.uint8)
        canvas[top:top+rh,left:left+rw]=cv2.resize(image,(rw,rh))[:,:,::-1]
        tensor=np.ascontiguousarray(canvas.transpose(2,0,1)[None],np.float32)/255
        output=self.door_detector.run(None,{self.door_detector.get_inputs()[0].name:tensor})[0][0]
        if output.shape!=(300,38):raise ValueError('不支持的门/镜面导出结构')
        objects=[]
        for row in output:
            if row[4]<threshold or int(row[5]) not in (0,1,4):continue
            x1,y1,x2,y2=(row[:4]-[left,top,left,top])/ratio
            objects.append({'bbox':[max(0,float(x1)),max(0,float(y1)),min(width,float(x2)),min(height,float(y2))],
                            'confidence':float(row[4]),'class_id':int(row[5]),'kind':{0:'doorway',1:'door',4:'mirror'}[int(row[5])]})
        # Reflection boxes should not become a second classroom entrance.
        mirrors=[d for d in objects if d['kind']=='mirror']
        from .behavior import iou
        return [d for d in objects if d['kind']=='mirror' or not any(iou(d['bbox'],x['bbox'])>.25 for x in mirrors)]

    def estimate_head_angles(self,image,box):
        if self.head_pose is None:return None
        x1,y1,x2,y2=box;w,h=x2-x1,y2-y1;ih,iw=image.shape[:2]
        crop=image[max(0,int(y1-.2*w)):min(ih,int(y2+.2*w)),max(0,int(x1-.2*h)):min(iw,int(x2+.2*h))]
        if crop.size==0:return None
        tensor=cv2.LUT(cv2.resize(crop[:,:,::-1],(224,224)),HEAD_LUT)
        tensor=np.ascontiguousarray(tensor.transpose(2,0,1)[None],np.float32)
        r=self.head_pose.run(None,{self.head_pose.get_inputs()[0].name:tensor})[0][0]
        if r.shape!=(3,3):raise ValueError('不支持的头姿导出结构')
        sy=np.hypot(r[0,0],r[1,0])
        pitch=np.arctan2(r[2,1],r[2,2]) if sy>=1e-6 else np.arctan2(-r[1,2],r[1,1])
        yaw=np.arctan2(-r[2,0],sy)
        roll=np.arctan2(r[1,0],r[0,0]) if sy>=1e-6 else 0
        return dict(zip(('pitch','yaw','roll'),map(float,np.degrees([pitch,yaw,roll]))))

    def detect_classroom_heads(self, image, threshold):
        """Fixed dual head/body export; head boxes become counting/tracking units.

        Export is not the raw (1,6,8400) advertised by the upstream example.
        RGB is validated against the local classroom diagnostic and recorded.
        """
        height, width = image.shape[:2]
        ratio = min(640 / width, 640 / height)
        rw, rh = int(width * ratio), int(height * ratio)
        left, top = (640 - rw) // 2, (640 - rh) // 2
        canvas = np.full((640,640,3),114,np.uint8)
        canvas[top:top+rh,left:left+rw] = cv2.resize(image,(rw,rh))[:,:,::-1]
        tensor = np.ascontiguousarray(canvas.transpose(2,0,1)[None],np.float32) / 255
        outputs = self.classroom_detector.run(None,{self.classroom_detector.get_inputs()[0].name:tensor})
        if len(outputs) != 3 or outputs[0].shape[-1] != 4:
            raise ValueError('不支持的头部模型导出格式')
        boxes, scores, classes = [o[0] for o in outputs]
        heads = []
        for class_id in (1,):
            selected = (classes[:,0] == class_id) & (scores[:,0] >= threshold)
            raw, confidence = boxes[selected], scores[selected,0]
            xy = (raw[:,:2]-raw[:,2:4]/2-[left,top])/ratio
            xywh = np.c_[xy,raw[:,2:4]/ratio]
            kept = cv2.dnn.NMSBoxes(xywh.tolist(),confidence.tolist(),threshold,.45)
            for i in np.asarray(kept).reshape(-1):
                x,y,w,h = xywh[i]
                box = [max(0,float(x)),max(0,float(y)),min(width,float(x+w)),min(height,float(y+h))]
                if box[2]-box[0] < 5 or box[3]-box[1] < 6:
                    continue
                cx = (box[0]+box[2])/2
                pose_box = [cx-1.5*w,box[1]-1.2*h,cx+1.5*w,box[1]+4*h]
                heads.append({'bbox':box,'pose_bbox':list(map(float,pose_box)), 'confidence':float(confidence[i]),
                              'class_id':0, 'target_kind':'head'})
        return heads

    def detect_classroom(self,image,threshold,heads=None):
        heads=self.detect_classroom_heads(image,threshold) if heads is None else heads
        # The COCO phone detector remains separate from the head counter.
        coco=self.detect(image,threshold)
        bodies=[d for d in coco if d['class_id']==0]
        for head in heads:
            x1,y1,x2,y2=head['bbox'];cx,cy=(x1+x2)/2,(y1+y2)/2;h=y2-y1
            possible=[b for b in bodies if b['bbox'][0]<=cx<=b['bbox'][2] and b['bbox'][1]<=cy<=b['bbox'][1]+.3*(b['bbox'][3]-b['bbox'][1]) and b['bbox'][3]-b['bbox'][1]>5*h]
            if possible:
                body=min(possible,key=lambda b:abs((b['bbox'][0]+b['bbox'][2])/2-cx)+abs(b['bbox'][1]-y1))
                head['body_bbox']=body['bbox']
        return heads + [d for d in coco if d['class_id']==67]

    def estimate_pose(self, image, bbox, classroom=False):
        pose_session = self.classroom_pose if classroom else self.pose
        tensor, geometry=self._pose_input(image,bbox,pose_session)
        outputs=pose_session.run(None,{pose_session.get_inputs()[0].name:tensor[None]})
        return self._decoded_output(outputs,0,geometry)

    @staticmethod
    def _pose_input(image,bbox,pose_session,tensor_out=None):
        input_height, input_width = pose_session.get_inputs()[0].shape[2:]
        x1, y1, x2, y2 = bbox
        cx, cy = (x1 + x2) / 2, (y1 + y2) / 2
        width, height = (x2 - x1) * 1.25, (y2 - y1) * 1.25
        width = max(width, height * input_width / input_height)
        height = width * input_height / input_width
        factor = input_width / width
        matrix = np.array([[factor, 0, input_width / 2 - cx * factor], [0, factor, input_height / 2 - cy * factor]], dtype=np.float32)
        crop = cv2.warpAffine(image, matrix, (input_width, input_height), flags=cv2.INTER_LINEAR)
        # Exact lookup of the original arithmetic for every uint8 pixel/channel.
        # This removes repeated float64 array conversion/subtraction/division.
        if crop.dtype==np.uint8:
            normalized=cv2.LUT(crop,POSE_LUT)
        else:
            normalized=(crop.astype(np.float32)-POSE_MEAN)/POSE_STD
        if tensor_out is None:
            tensor = np.ascontiguousarray(normalized.transpose(2, 0, 1), dtype=np.float32)
        else:
            np.copyto(tensor_out,normalized.transpose(2,0,1))
            tensor=tensor_out
        return tensor,(input_width,input_height,width,height,cx,cy)

    @staticmethod
    def _pose_output(simcc_x,simcc_y,geometry,scores=None):
        input_width,input_height,width,height,cx,cy=geometry
        if scores is None:
            px, py = simcc_x.argmax(axis=1) / 2, simcc_y.argmax(axis=1) / 2
            scores = np.minimum(simcc_x.max(axis=1), simcc_y.max(axis=1))
        else:px,py=simcc_x/2,simcc_y/2
        points = np.stack((px / input_width * width + cx - width / 2,
                           py / input_height * height + cy - height / 2), axis=1)
        return points, np.clip(scores, 0, 1)

    def _decoded_output(self,outputs,index,geometry):
        if len(outputs)==2:return self._pose_output(outputs[0][index],outputs[1][index],geometry)
        return self._pose_output(outputs[0][index],outputs[1][index],geometry,outputs[2][index])

    def estimate_poses(self,image,boxes,classroom=False,cancel=None):
        if cancel:cancel()
        # Only reuse exactly identical crops in this image, never nearby times or boxes.
        # Retaining the object prevents Python reusing an image id for another frame.
        if getattr(self,'_cached_image',None) is not image:
            self._cached_image=image;self._frame_poses={}
        cache=getattr(self,'_frame_poses',{})
        missing=[];keys=[];seen=set()
        for box in boxes:
            key=(classroom,tuple(box));keys.append(key)
            if key not in cache and key not in seen:
                missing.append(box);seen.add(key)
        if not hasattr(self,'pose_metrics'):self.pose_metrics={}
        self.pose_metrics['requested_people']=self.pose_metrics.get('requested_people',0)+len(boxes)
        self.pose_metrics['reused_exact_crops']=self.pose_metrics.get('reused_exact_crops',0)+len(boxes)-len(missing)
        outputs=self._estimate_unique_poses(image,missing,classroom,cancel)
        for box,output in zip(missing,outputs):cache[(classroom,tuple(box))]=output
        self._frame_poses=cache
        return [cache[key] for key in keys]

    def _estimate_unique_poses(self,image,boxes,classroom=False,cancel=None):
        """Same crops and decoding, reordered only at the model execution layer."""
        pose_session=self.classroom_pose if classroom else self.pose
        results=[]
        size=max(1,self.pose_batch_size)
        fixed=getattr(self,'fixed_pose_batches',False)
        size=min(size,32 if fixed else 64)
        if not fixed and pose_session.get_inputs()[0].shape[0]==1:size=1
        start=0
        while start<len(boxes):
            if cancel:cancel()
            remaining=len(boxes)-start
            group_size=min(size,remaining)
            # Avoid padding twenty people to 32; use 16+4 instead.
            adaptive=bool(int(os.environ.get('VISION_ADAPTIVE_BATCH',runtime_settings.get('adaptive_pose_batches',0))))
            if fixed and adaptive and remaining<size:
                group_size=max(n for n in (1,2,4,8,16,32) if n<=group_size)
            group=boxes[start:start+group_size];start+=group_size
            if size==1 or self.pose_batch_size==1:
                results.extend(self.estimate_pose(image,b,classroom) for b in group)
                continue
            started=time.perf_counter()
            buckets=(1,2,4,8,16,32) if adaptive else (8,16,32,64)
            bucket=next(n for n in buckets if n>=len(group)) if fixed else len(group)
            buffered=getattr(self,'reuse_pose_buffers',False)
            if buffered:
                buffer_key=(classroom,bucket)
                if buffer_key not in self._pose_buffers:
                    self._pose_buffers[buffer_key]=np.empty((bucket,*pose_session.get_inputs()[0].shape[1:]),np.float32)
                tensor=self._pose_buffers[buffer_key]
                inputs=[self._pose_input(image,b,pose_session,tensor[i]) for i,b in enumerate(group)]
                if len(group)<bucket:tensor[len(group):]=tensor[0]
            else:
                inputs=[self._pose_input(image,b,pose_session) for b in group]
                tensor=np.stack([item[0] for item in inputs])
            self.pose_metrics['preprocess_seconds']=self.pose_metrics.get('preprocess_seconds',0)+time.perf_counter()-started
            try:
                started=time.perf_counter()
                runtime=pose_session
                if fixed:
                    key=(classroom,bucket)
                    if bucket==1:
                        runtime=pose_session
                    elif key not in self._pose_sessions:
                        name=getattr(self,'pose_files',{False:'rtmpose_s.onnx',True:'rtmpose_x.onnx'})[classroom]
                        self._pose_sessions[key]=session(MODELS/name,True,bucket)
                    if bucket!=1:runtime=self._pose_sessions[key]
                    if len(group)<bucket and not buffered:
                        tensor=np.concatenate([tensor,np.repeat(tensor[:1],bucket-len(group),axis=0)])
                    self.provenance['active_pose_buckets']=sorted({k[1] for k in self._pose_sessions})
                outputs=runtime.run(None,{runtime.get_inputs()[0].name:tensor})
                self.pose_metrics['inference_and_compile_seconds']=self.pose_metrics.get('inference_and_compile_seconds',0)+time.perf_counter()-started
            except Exception as error:
                # Unsupported batches / GPU memory pressure must not drop people.
                self.pose_batch_size=1
                self.provenance['batch_fallback']=str(error)[:300]
                results.extend(self.estimate_pose(image,b,classroom) for b in group)
                continue
            started=time.perf_counter()
            results.extend(self._decoded_output(outputs,i,item[1]) for i,item in enumerate(inputs))
            self.pose_metrics['decode_seconds']=self.pose_metrics.get('decode_seconds',0)+time.perf_counter()-started
        return results


def get_models():
    global _instance
    with _lock:
        if _instance is None:
            _instance = VisionModels()
    return _instance
