"""pose: finds people's pose, hands and hand gestures in camera frames, with MediaPipe on the CPU.

camview runs it while someone watches its page (camview -web -analyze '...'). Protocol, on stdin and stdout:
  in:  a frame is a 4-byte big-endian length, then that many bytes of JPEG
  out: one JSON line per frame:
       {"ms": time spent, "w": width, "h": height,
        "pose": [[x, y, visibility] x 33] or null,      x and y from 0 to 1, as in the picture (not mirrored)
        "poses": ["left arm up", ...],                  simple poses worked out from the points
        "hands": [{"side": "left", "gesture": "thumbs up", "score": 0.93, "points": [[x, y] x 21]}]}
Sides are the person's own. Nothing is stored and nothing leaves the process.

Environment: POSE_MODEL (full or lite, default full), WIDTH (frames are scaled to this width first, default 640).
"""

import json
import os
import struct
import sys
import time

import cv2
import mediapipe as mp
import numpy as np
from mediapipe.tasks.python import BaseOptions, vision

MODELS = os.environ.get("MODELS_DIR", "/models")
WIDTH = int(os.environ.get("WIDTH", "640"))

GESTURES = {
    "Thumb_Up": "thumbs up", "Thumb_Down": "thumbs down", "Open_Palm": "open palm", "Closed_Fist": "fist",
    "Pointing_Up": "pointing up", "Victory": "victory", "ILoveYou": "I love you",
}

# pose landmark numbers (MediaPipe's): nose, then left/right shoulder, elbow, wrist, hip
NOSE, L_SH, R_SH, L_EL, R_EL, L_WR, R_WR, L_HIP, R_HIP = 0, 11, 12, 13, 14, 15, 16, 23, 24


def poses(p):
    """Simple poses from the pose points (each [x, y, visibility]); y grows downwards."""
    seen = lambda *i: all(p[k][2] > 0.5 for k in i)
    out = []
    up = {}
    for side, wr, sh in (("left", L_WR, L_SH), ("right", R_WR, R_SH)):
        # a hand above the head
        up[side] = seen(wr, sh, NOSE) and p[wr][1] < p[NOSE][1]
    if up["left"] and up["right"]:
        out.append("both arms up")
    elif up["left"]:
        out.append("left arm up")
    elif up["right"]:
        out.append("right arm up")
    if seen(L_WR, R_WR, L_EL, R_EL, L_SH, R_SH):
        width = abs(p[L_SH][0] - p[R_SH][0])
        level = all(abs(p[w][1] - p[s][1]) < 0.6 * width and abs(p[e][1] - p[s][1]) < 0.6 * width
                    for w, e, s in ((L_WR, L_EL, L_SH), (R_WR, R_EL, R_SH)))
        wide = abs(p[L_WR][0] - p[R_WR][0]) > 2.5 * width
        if level and wide:
            out.append("T-pose")
    if seen(L_WR, R_WR, L_SH, R_SH, L_HIP, R_HIP):
        # both hands on the hips: wrists near the hips, elbows out
        near = all(abs(p[w][0] - p[h][0]) < 0.12 and abs(p[w][1] - p[h][1]) < 0.12 for w, h in ((L_WR, L_HIP), (R_WR, R_HIP)))
        if near:
            out.append("hands on hips")
    return out


def main():
    pose_model = "pose_landmarker_%s.task" % os.environ.get("POSE_MODEL", "full")
    mode = vision.RunningMode.VIDEO  # frames in order: tracking between frames, faster and steadier
    pose = vision.PoseLandmarker.create_from_options(vision.PoseLandmarkerOptions(
        base_options=BaseOptions(model_asset_path=os.path.join(MODELS, pose_model)), running_mode=mode, num_poses=1))
    hands = vision.GestureRecognizer.create_from_options(vision.GestureRecognizerOptions(
        base_options=BaseOptions(model_asset_path=os.path.join(MODELS, "gesture_recognizer.task")), running_mode=mode, num_hands=2))
    print(json.dumps({"ready": True, "pose_model": pose_model}), flush=True)

    stdin, last_ts = sys.stdin.buffer, 0
    while True:
        head = stdin.read(4)
        if len(head) < 4:
            return
        (n,) = struct.unpack(">I", head)
        data = stdin.read(n)
        if len(data) < n:
            return
        start = time.monotonic()
        img = cv2.imdecode(np.frombuffer(data, np.uint8), cv2.IMREAD_COLOR)
        if img is None:
            print(json.dumps({"error": "not a JPEG"}), flush=True)
            continue
        h, w = img.shape[:2]
        if w > WIDTH:
            img = cv2.resize(img, (WIDTH, h * WIDTH // w), interpolation=cv2.INTER_AREA)
        frame = mp.Image(image_format=mp.ImageFormat.SRGB, data=cv2.cvtColor(img, cv2.COLOR_BGR2RGB))
        ts = max(int(time.monotonic() * 1000), last_ts + 1)  # VIDEO mode wants rising timestamps
        last_ts = ts

        out = {"w": w, "h": h, "pose": None, "poses": [], "hands": []}
        pr = pose.detect_for_video(frame, ts)
        if pr.pose_landmarks:
            p = [[round(l.x, 4), round(l.y, 4), round(l.visibility or 0, 2)] for l in pr.pose_landmarks[0]]
            out["pose"], out["poses"] = p, poses(p)
        gr = hands.recognize_for_video(frame, ts)
        for i, pts in enumerate(gr.hand_landmarks):
            g = gr.gestures[i][0] if gr.gestures and gr.gestures[i] else None
            side = gr.handedness[i][0].category_name.lower() if gr.handedness and gr.handedness[i] else ""
            # MediaPipe names the side as seen in a mirrored selfie picture; this picture is not mirrored
            side = {"left": "right", "right": "left"}.get(side, side)
            out["hands"].append({
                "side": side,
                "gesture": GESTURES.get(g.category_name, "") if g else "",
                "score": round(g.score, 2) if g else 0,
                "points": [[round(l.x, 4), round(l.y, 4)] for l in pts],
            })
        out["ms"] = round((time.monotonic() - start) * 1000, 1)
        print(json.dumps(out, separators=(",", ":")), flush=True)


if __name__ == "__main__":
    main()
