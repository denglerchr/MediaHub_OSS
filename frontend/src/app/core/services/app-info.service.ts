import { Injectable } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { BehaviorSubject, Observable, of } from 'rxjs';
import { tap, catchError, finalize, shareReplay } from 'rxjs/operators';
import { AppInfo } from '../models';

@Injectable({
  providedIn: 'root',
})
export class AppInfoService {
  private readonly apiUrl = '/api/info';

  private infoSubject = new BehaviorSubject<AppInfo | null>(null);
  public info$ = this.infoSubject.asObservable();

  // FE-030: concurrent consumers (sidebar + login page) used to fire one
  // GET /api/info each — share the in-flight request instead.
  private inFlight$: Observable<AppInfo | null> | null = null;

  constructor(private http: HttpClient) {}

  loadInfo(): Observable<AppInfo | null> {
    if (this.infoSubject.value) {
      return of(this.infoSubject.value);
    }

    if (this.inFlight$) {
      return this.inFlight$;
    }

    this.inFlight$ = this.http.get<AppInfo>(this.apiUrl).pipe(
      tap((info) => {
        this.infoSubject.next(info);
      }),
      catchError((err) => {
        console.error('Failed to load app info:', err);
        this.infoSubject.next(null);
        return of(null);
      }),
      finalize(() => {
        this.inFlight$ = null;
      }),
      // FE-030: late subscribers of the same request share its single response.
      shareReplay(1),
    );

    return this.inFlight$;
  }
}
