#pragma once
#include <stdint.h>

typedef struct {
    uint16_t pos;
    uint8_t bank;
    uint8_t flags;
} vgmswan_state_t;

#define VGMSWAN_PLAYBACK_FINISHED 0xFFFF

void vgmswan_init(vgmswan_state_t *state, uint8_t bank, uint8_t song_id);
// return: amount of HBLANK lines to wait
uint16_t vgmswan_play(vgmswan_state_t *state);