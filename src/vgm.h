#pragma once
#include <stdint.h>

#define VGMSWAN_DAC_FREQ_4000 0x00
#define VGMSWAN_DAC_FREQ_6000 0x01
#define VGMSWAN_DAC_FREQ_12000 0x02
#define VGMSWAN_DAC_FREQ_24000 0x03

#define VGMSWAN_MODE_WONDERSWAN 0x00

#if defined(VGMSWAN_MODE_WONDERSWAN)
# define VGMSWAN_USE_PCM
# define VGMSWAN_MAX_STREAMS 1
# define VGMSWAN_MAX_DATA_BLOCKS 16
#endif

typedef struct {
    uint8_t bank, start_bank;
    uint16_t pos, start_pos;
    uint8_t flags;

#ifdef VGMSWAN_USE_PCM
    uint8_t pcm_data_block_count;
    uint16_t pcm_data_block_location;
    uint16_t pcm_data_block_offset[VGMSWAN_MAX_DATA_BLOCKS + 1];

    /* uint8_t pcm_stream_ctrl_a[VGMSWAN_MAX_STREAMS];
    uint8_t pcm_stream_ctrl_d[VGMSWAN_MAX_STREAMS]; */
    uint8_t pcm_stream_flags[VGMSWAN_MAX_STREAMS];
#endif
} vgmswan_state_t;

#define VGMSWAN_PLAYBACK_FINISHED 0xFFFF

void vgmswan_init(vgmswan_state_t *state, uint8_t bank, uint16_t pos);
// return: amount of HBLANK lines to wait
uint16_t vgmswan_play(vgmswan_state_t *state);