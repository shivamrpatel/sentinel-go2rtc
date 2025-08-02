import datetime
import logging
import os

import cv2
import numpy as np

RESIZE_DIMENSIONS = (640, 480)

logging.basicConfig(
    level=logging.INFO, format="%(asctime)s [%(levelname)s] %(message)s"
)


def quantify_motion_farneback(flow, motion_threshold=1.0):
    if flow is None:
        return 0.0

    # Calculate magnitude from the 2-channel flow array
    magnitude, _ = cv2.cartToPolar(flow[..., 0], flow[..., 1])

    # Sum of magnitudes of flow vectors that are above a certain internal threshold
    significant_motion_magnitudes = magnitude[magnitude > motion_threshold]

    motion = np.sum(significant_motion_magnitudes)

    return motion


def save_video_segment(frames, output_path, fps, frame_size):
    if not frames:
        return

    fourcc = cv2.VideoWriter.fourcc(*"mp4v")  # Codec for .mp4
    out = cv2.VideoWriter(output_path, fourcc, fps, frame_size)

    for frame in frames:
        if frame is not None and frame.shape[1::-1] == frame_size:
            out.write(frame)
        else:
            logging.warning(
                f"Frame is None or has wrong size, skipping. Frame shape: {frame.shape if frame is not None else 'None'}, Expected size: {frame_size}"
            )
    out.release()
    logging.info(f"Saved segment: {output_path}")


def find_important_parts_optical_flow_farneback(
    video_path,
    output_dir="motion_events_farneback",
    motion_score_threshold=50000.0,  # This will need tuning! Farneback magnitudes are typically larger.
    event_min_duration_frames=15,  # Minimum number of frames for an event to be considered (e.g., 0.5s at 30 FPS)
    buffer_frames=5,  # Add a few frames before/after the detected motion burst
    max_gap_frames=10,  # Max frames of low motion to bridge gaps within an event
):
    if not os.path.exists(output_dir):
        os.makedirs(output_dir)

    cap = cv2.VideoCapture(video_path)
    if not cap.isOpened():
        logging.warning(f"Error: Could not open video {video_path}")
        return

    fps = int(cap.get(cv2.CAP_PROP_FPS))
    original_width = int(cap.get(cv2.CAP_PROP_FRAME_WIDTH))
    original_height = int(cap.get(cv2.CAP_PROP_FRAME_HEIGHT))
    original_frame_size = (original_width, original_height)

    ret, prev_original_frame = cap.read()
    if not ret:
        logging.warning("Error: Could not read the first frame.")
        cap.release()
        return

    # Resize the first frame for optical flow calculation
    prev_resized_frame = cv2.resize(
        prev_original_frame, RESIZE_DIMENSIONS, interpolation=cv2.INTER_AREA
    )
    prev_gray_resized = cv2.cvtColor(prev_resized_frame, cv2.COLOR_BGR2GRAY)

    current_segment_frames = []  # Stores original resolution frames
    recording_event = False
    frames_since_last_motion = 0
    event_start_frame_idx = -1  # Index of the frame where the event officially started
    frame_count = 0

    logging.info(
        f"Starting motion detection for {video_path} using Farneback Optical Flow..."
    )
    logging.info(
        f"Optical flow calculated on {RESIZE_DIMENSIONS} frames, saving original {original_frame_size} frames."
    )

    while True:
        ret, current_original_frame = cap.read()
        if not ret:
            break

        # Resize the current frame for optical flow calculation
        current_resized_frame = cv2.resize(
            current_original_frame, RESIZE_DIMENSIONS, interpolation=cv2.INTER_AREA
        )
        current_gray_resized = cv2.cvtColor(current_resized_frame, cv2.COLOR_BGR2GRAY)

        # Calculate dense optical flow using Farneback method on the resized grayscale frames
        flow = cv2.calcOpticalFlowFarneback(
            prev_gray_resized,
            current_gray_resized,
            None,  # Output flow
            0.5,  # pyr_scale: image scale to build pyramids
            3,  # levels: number of pyramid levels
            15,  # winsize: averaging window size
            3,  # iterations: number of iterations at each pyramid level
            5,  # poly_n: size of the pixel neighborhood
            1.2,  # poly_sigma: standard deviation of the Gaussian
            0,  # flags: cv2.OPTFLOW_FARNEBACK_GAUSSIAN for more accurate
        )

        motion_score = quantify_motion_farneback(
            flow, motion_threshold=1.0
        )  # Internal threshold for pixel movement

        # Debugging: print motion score every 60 frames
        if frame_count % 60 == 0:
            logging.debug(f"Frame {frame_count}: Motion Score = {motion_score:.2f}")

        # --- Motion Detection Logic ---
        if motion_score > motion_score_threshold:
            frames_since_last_motion = 0  # Reset gap counter
            if not recording_event:
                # Start a new event
                logging.info(
                    f"Motion detected at frame {frame_count} (Score: {motion_score:.2f}) - Starting event."
                )
                recording_event = True
                event_start_frame_idx = frame_count

                # Add buffer frames before the event
                # We need to re-read these from the original video capture
                cap_buffer = cv2.VideoCapture(video_path)
                cap_buffer.set(
                    cv2.CAP_PROP_POS_FRAMES,
                    max(0, event_start_frame_idx - buffer_frames),
                )
                current_segment_frames = []  # Reset for new segment
                for _ in range(
                    max(0, event_start_frame_idx - buffer_frames), event_start_frame_idx
                ):
                    _, bf = cap_buffer.read()
                    if bf is not None:
                        current_segment_frames.append(bf)
                cap_buffer.release()

            current_segment_frames.append(
                current_original_frame
            )  # Add current motion frame (original resolution)

        elif recording_event:
            # We are in an event, but current frame has low motion
            frames_since_last_motion += 1
            if frames_since_last_motion <= max_gap_frames:
                # Still within the allowed gap, keep recording the original frame
                current_segment_frames.append(current_original_frame)
            else:
                # Gap exceeded, end the current event
                if len(current_segment_frames) >= event_min_duration_frames:
                    # Add buffer frames after the event (from the segment itself)
                    # Be careful not to exceed the actual frames collected in current_segment_frames
                    # The buffer frames after the event are already included in current_segment_frames
                    # up to the point where frames_since_last_motion started counting.
                    # So, we take the segment up to (current frame - frames_since_last_motion) + buffer_frames
                    segment_to_save = current_segment_frames[
                        : len(current_segment_frames)
                        - frames_since_last_motion
                        + buffer_frames
                    ]
                    timestamp = datetime.datetime.now().strftime("%Y%m%d_%H%M%S")
                    output_filename = os.path.join(
                        output_dir,
                        f"motion_event_{event_start_frame_idx}-{frame_count - 1}_{timestamp}.mp4",
                    )
                    save_video_segment(
                        segment_to_save, output_filename, fps, original_frame_size
                    )
                else:
                    logging.info(
                        f"Event too short (frames: {len(current_segment_frames)}), discarded."
                    )

                logging.info(f"Motion event ended at frame {frame_count}.")
                recording_event = False
                current_segment_frames = []
                frames_since_last_motion = 0  # Reset gap counter

        prev_gray_resized = current_gray_resized.copy()
        frame_count += 1

    # Save any remaining segment if the video ends during an event
    if recording_event and len(current_segment_frames) >= event_min_duration_frames:
        timestamp = datetime.datetime.now().strftime("%Y%m%d_%H%M%S")
        output_filename = os.path.join(
            output_dir,
            f"motion_event_{event_start_frame_idx}-{frame_count - 1}_{timestamp}.mp4",
        )
        save_video_segment(
            current_segment_frames, output_filename, fps, original_frame_size
        )
    elif recording_event:
        logging.info(
            f"Final event too short (frames: {len(current_segment_frames)}), discarded."
        )

    cap.release()
    logging.info("Video processing complete.")


logging.info("Starting...")

input_video = "clip3.mp4"
find_important_parts_optical_flow_farneback(
    input_video,
    output_dir="important_parts",
    # motion_score_threshold=1000,
    event_min_duration_frames=10,
    buffer_frames=5,
    max_gap_frames=90,
)
