package internal

import (
	"bytes"
	"context"
	"errors"
	"gopkg.in/tomb.v2"
	"os"
	"syscall"
)

func PipeOutlet(ctx context.Context, commonSession muxSession) (error) {
	var err error
	var t *tomb.Tomb
	t, ctx = tomb.WithContext(ctx)
	ignoreSignalDefault(syscall.SIGPIPE)
	defer unIgnoreSignalDefault(syscall.SIGPIPE)
	ignoreSignalDefault(syscall.SIGINT)
	defer unIgnoreSignalDefault(syscall.SIGINT)
	defer func() {
		if err := recover(); err != nil {
			panic(err)
		}
		t.Kill(nil)
		t.Wait()
	}()
	go func() {
		// handle signal goroutine
		select {
		case <- signalSIGINT:
			t.Kill(nil)
		case <- t.Dead():
		}
	}()
	theStream, err := commonSession.AcceptStream()
	if err != nil {
		log.Errorf("cannot open stream on common session")
		return err
	}
	defer func() {
		err = theStream.Close()
		if err != nil{
			log.Warning(err)
		}
	}()
	// confirm each side identity
	{
		n, err := theStream.Write([]byte(ModePipeOutlet))
		if err != nil || n != ModeStringLen {
			log.Error("unable to send identity")
			return errors.New("unable to send identity")
		}
		var buf = make([]byte, ModeStringLen)
		_, err = theStream.Read(buf)
		if err != nil || !bytes.Equal(buf, []byte(ModePipeInlet)) {
			log.Error("unable to confirm peer identity")
			return errors.New("unable to confirm peer identity")
		}
		log.Debug("identity confirmed")
	}

	t.Go(func() error {
		defer t.Kill(nil)
		ReaderToWriter(theStream, os.Stdout)
		return nil
	})

	<- t.Dying()
	log.Debug("pipe closing")

	return nil
}
