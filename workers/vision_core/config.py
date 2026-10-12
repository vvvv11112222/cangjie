from pathlib import Path
import os
from typing import Literal
from pydantic import BaseModel, Field, model_validator

ROOT = Path(__file__).resolve().parents[1]
DATA = Path(os.environ.get("VISION_DATA_ROOT", ROOT.parent / "var" / "vision"))
MODELS = Path(os.environ.get("VISION_MODEL_ROOT", ROOT.parent / "var" / "vision-models"))
VERSION = "vision-lab-0.4.3"
MAX_UPLOAD = 4 * 1024**3


class Rect(BaseModel):
    """Normalized coordinates in the displayed, autorotated image."""
    x1: float = Field(ge=0, le=1)
    y1: float = Field(ge=0, le=1)
    x2: float = Field(ge=0, le=1)
    y2: float = Field(ge=0, le=1)

    @model_validator(mode="after")
    def check_order(self):
        if self.x1 >= self.x2 or self.y1 >= self.y2:
            raise ValueError("区域必须具有正宽度和正高度")
        return self


class AnalysisConfig(BaseModel):
    title: str = Field(default="离线课堂视觉分析", max_length=100)
    sampling_fps: float = Field(default=2, ge=0.5, le=10)
    start_s: float = Field(default=0, ge=0)
    duration_s: float = Field(default=60, ge=0, le=7200)  # 0 = to end
    confidence: float = Field(default=0.25, ge=0.1, le=0.9)
    detection_mode: Literal['classroom', 'general'] = 'classroom'
    exclude_rois: list[Rect] = Field(default_factory=list, max_length=20)
    student_roi: Rect | None = None
    seat_rois: list[Rect] = Field(default_factory=list, max_length=80)
    door_roi: Rect | None = None
    inside_direction: Literal["right", "left"] = "right"
    class_start_s: float = Field(default=0, ge=0)
    class_end_s: float | None = Field(default=None, gt=0)
    grace_s: float = Field(default=60, ge=0, le=1800)
    head_down_enabled: bool = True
    head_pitch_down_deg: float = Field(default=25,ge=10,le=60)
    auto_seats: bool = True
    auto_door: bool = True
    schedule_enabled: bool = False
    max_pose_people: int = Field(default=80, ge=1, le=80)
    hand_min_s: float = Field(default=0.5, ge=0.3, le=10)
    leave_min_s: float = Field(default=2, ge=0.5, le=30)
    head_min_s: float = Field(default=3, ge=1, le=60)
    phone_min_s: float = Field(default=1, ge=0.5, le=15)
    max_samples: int = Field(default=15000, ge=10, le=30000)

    @model_validator(mode="after")
    def check_schedule(self):
        if self.class_end_s is not None and self.class_end_s <= self.class_start_s:
            raise ValueError("课堂结束位置必须晚于开始位置")
        return self
