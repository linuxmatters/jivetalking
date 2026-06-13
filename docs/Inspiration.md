# Inspiration

Before I wrote a line of Jivetalking, I went to school on a handful of classic
audio devices. I read everything I could find on how they worked, then tried to
rebuild each one as faithfully as FFmpeg would let me. That exercise, the
research followed by the stubborn attempt to reproduce the thing, was the real
tuition. These devices were my teachers. They showed me how good spoken-word
audio processing is supposed to behave.

Four lessons stuck.

**Dolby SR** taught me Ray Dolby's principle of least treatment: intervene only
as much as the signal genuinely needs, and no more. My first noise reduction was
a Dolby SR-inspired compander built on exactly that idea. It is gone now,
replaced by FFT-based denoise that does the job better, but the lesson outlived
the method. Touch the audio lightly.

**The Drawmer DS201** taught me frequency-conscious gating. A gate should clean
the gaps between words without chewing on the voice itself, and the trick is to
let it listen to the frequencies that matter while staying deaf to the ones that
do not. Clean silence between phrases, an untouched voice within them.

**The Teletronix LA-2A** taught me to prefer gentle, programme-dependent
levelling over heavy-handed compression. Its optical cell eased the loud
passages down so smoothly you stopped noticing it was working. The goal is to
even out the level, not to flatten the life out of a delivery.

**The CBS Volumax** taught me transparency. A limiter exists to stop peaks
running away, and the best one is the one nobody hears. No pumping, no dulled
consonants, no processed sheen on the voice. The presenter reaches the listener,
the limiter stays out of the way.

Jivetalking no longer reproduces any of these circuits. The implementations have
moved on, and what runs today owes more to measurement and adaptation than to any
one piece of vintage hardware. But the lessons remain the brief: least treatment,
gentle levelling, frequency-conscious cleanup, and limiting you cannot hear. The
teachers are no longer in the room, yet I am still trying to earn their marks.
